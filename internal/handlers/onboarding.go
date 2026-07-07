package handlers

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/ledgefice/internal/database"
	"github.com/ledgefice/internal/middleware"
	"github.com/ledgefice/internal/models"
	"github.com/ledgefice/internal/services"
	"github.com/ledgefice/pkg/utils"
	"golang.org/x/crypto/bcrypt"
)

type OnboardingHandler struct {
	Images *services.ImageService
	Nomba  *services.NombaService
}

type onboardingInput struct {
	OrganizationName string `form:"organization_name"`
	Email            string `form:"email"`
	Password         string `form:"password"`
	NumberOfWorkers  int    `form:"number_of_workers"`
	Plan             string `form:"plan"`
}

// Setup NEVER creates an Organization/User/Department directly — every plan
// requires a paid subscription, so we create a PendingSignup and redirect the
// user to Nomba's checkout page. Once payment is confirmed, the webhook will
// create the actual Organization/User/Department.
func (h *OnboardingHandler) Setup(c *fiber.Ctx) error {
	var input onboardingInput
	if err := c.BodyParser(&input); err != nil {
		return utils.BadRequest(c, "invalid request body")
	}
	if input.OrganizationName == "" || input.Email == "" || input.Password == "" {
		return utils.BadRequest(c, "organization_name, email, and password are required")
	}
	if len(input.Password) < 8 {
		return utils.BadRequest(c, "password must be at least 8 characters")
	}

	plan := models.PlanType(strings.ToLower(input.Plan))
	if _, ok := models.PlanConfigs[plan]; !ok {
		plan = models.PlanStarter
	}

	email := strings.ToLower(strings.TrimSpace(input.Email))

	// Check email not already taken — by an existing user OR an unpaid pending
	// signup. Without the second check, someone could spam checkout links
	// against the same email indefinitely without ever paying.
	var existing models.User
	if err := database.DB.Where("email = ?", email).First(&existing).Error; err == nil {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "email already in use"})
	}
	var existingPending models.PendingSignup
	if err := database.DB.Where("email = ?", email).First(&existingPending).Error; err == nil {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error": "a signup for this email is already awaiting payment",
		})
	}

	// Handle optional logo upload
	logoURL := ""
	fileHeader, err := c.FormFile("logo")
	if err == nil && fileHeader != nil {
		ext := strings.ToLower(fileHeader.Filename)
		allowed := map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true}
		if !allowed[ext[len(ext)-4:]] && !allowed[ext[len(ext)-5:]] {
			return utils.BadRequest(c, "logo must be jpg, jpeg, png, or webp")
		}
		file, err := fileHeader.Open()
		if err != nil {
			return utils.InternalError(c, err)
		}
		url, _, err := h.Images.Upload(file, fileHeader, "ledgefice/logos")
		if err != nil {
			return utils.InternalError(c, err)
		}
		logoURL = url
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return utils.InternalError(c, err)
	}

	cfg := models.GetPlanConfig(plan)

	namePart := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(input.OrganizationName)), " ", "")
	orderRef := fmt.Sprintf("sub_%s_%d", namePart[:min(8, len(namePart))], time.Now().Unix())
	amountNaira := float64(cfg.MonthlyPrice) / 100.0 // kobo -> naira

	result, err := h.Nomba.CreateCheckoutOrder(services.CheckoutOrderInput{
		OrderReference: orderRef,
		CustomerEmail:  email,
		Amount:         amountNaira,
		Currency:       "NGN",
		CallbackURL:    os.Getenv("APP_BASE_URL") + "/payments/nomba/callback",
		TokenizeCard:   true,
	})
	if err != nil {
		return utils.InternalError(c, err)
	}

	pending := models.PendingSignup{
		OrganizationName: strings.TrimSpace(input.OrganizationName),
		Email:            email,
		PasswordHash:     string(hash),
		LogoURL:          logoURL,
		NumberOfWorkers:  input.NumberOfWorkers,
		Plan:             plan,
		OrderReference:   orderRef,
		Amount:           amountNaira,
		Currency:         "NGN",
	}
	if err := database.DB.Create(&pending).Error; err != nil {
		return utils.InternalError(c, err)
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"message":         "checkout created — your workspace will be set up once payment is confirmed",
		"checkout_link":   result.CheckoutLink,
		"order_reference": orderRef,
	})
}


func (h *OnboardingHandler) GetMe(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)

	var org models.Organization
	if err := database.DB.First(&org, "id = ?", orgID).Error; err != nil {
		return utils.NotFound(c, "organization not found")
	}

	planCfg := models.GetPlanConfig(org.Plan)

	return utils.OK(c, fiber.Map{
		"id":                org.ID,
		"name":              org.Name,
		"logo_url":          org.LogoURL,
		"number_of_workers": org.NumberOfWorkers,
		"plan":              org.Plan,
		"owner_id":          org.OwnerID,
		"limits": fiber.Map{
			"max_departments": planCfg.MaxDepartments,
			"max_users":       planCfg.MaxUsers,
		},
		"features": planCfg.Features,
	})
}


func (h *OnboardingHandler) UpdateMe(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)
	userID := middleware.CurrentUserID(c)

	var org models.Organization
	if err := database.DB.First(&org, "id = ?", orgID).Error; err != nil {
		return utils.NotFound(c, "organization not found")
	}

	// Only the org owner can edit organization details.
	if org.OwnerID == nil || *org.OwnerID != userID {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "only the organization owner can update these details"})
	}

	changed := []string{}

	name := strings.TrimSpace(c.FormValue("name"))
	if name != "" && name != org.Name {
		var existingOrg models.Organization
		if err := database.DB.Where("name = ? AND id != ?", name, org.ID).First(&existingOrg).Error; err == nil {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "organization name already in use"})
		}
		org.Name = name
		changed = append(changed, "name")
	}

	if workersStr := c.FormValue("number_of_workers"); workersStr != "" {
		n, perr := parsePositiveInt(workersStr)
		if perr != nil {
			return utils.BadRequest(c, "number_of_workers must be a valid positive integer")
		}
		org.NumberOfWorkers = n
		changed = append(changed, "number_of_workers")
	}

	fileHeader, ferr := c.FormFile("logo")
	if ferr == nil && fileHeader != nil {
		file, err := fileHeader.Open()
		if err != nil {
			return utils.BadRequest(c, "could not open uploaded logo")
		}
		url, _, err := h.Images.Upload(file, fileHeader, "ledgefice/logos")
		if err != nil {
			return utils.InternalError(c, err)
		}
		org.LogoURL = url
		changed = append(changed, "logo")
	}

	if len(changed) == 0 {
		return utils.BadRequest(c, "no changes provided")
	}

	if err := database.DB.Save(&org).Error; err != nil {
		return utils.InternalError(c, err)
	}

	services.WriteAudit(services.AuditInput{
		OrganizationID: &org.ID,
		ActorID:        &userID,
		ActorEmail:     middleware.CurrentEmail(c),
		Action:         models.AuditActionUpdate,
		Module:         models.AuditModuleSystem,
		ResourceID:     org.ID.String(),
		Description:    "Organization details updated (" + strings.Join(changed, ", ") + ").",
		IPAddress:      middleware.ClientIP(c),
		UserAgent:      c.Get("User-Agent"),
	})

	planCfg := models.GetPlanConfig(org.Plan)

	return utils.OK(c, fiber.Map{
		"id":                org.ID,
		"name":              org.Name,
		"logo_url":          org.LogoURL,
		"number_of_workers": org.NumberOfWorkers,
		"plan":              org.Plan,
		"owner_id":          org.OwnerID,
		"limits": fiber.Map{
			"max_departments": planCfg.MaxDepartments,
			"max_users":       planCfg.MaxUsers,
		},
		"features": planCfg.Features,
	})
}

func parsePositiveInt(s string) (int, error) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("invalid number: %s", s)
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}

type changePasswordInput struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (h *AuthHandler) ChangePassword(c *fiber.Ctx) error {
	userID := middleware.CurrentUserID(c)

	var input changePasswordInput
	if err := c.BodyParser(&input); err != nil {
		return utils.BadRequest(c, "invalid request body")
	}
	if input.CurrentPassword == "" || input.NewPassword == "" {
		return utils.BadRequest(c, "current_password and new_password are required")
	}
	if len(input.NewPassword) < 8 {
		return utils.BadRequest(c, "new password must be at least 8 characters")
	}
	if input.CurrentPassword == input.NewPassword {
		return utils.BadRequest(c, "new password must be different from current password")
	}

	var user models.User
	if err := database.DB.First(&user, "id = ?", userID).Error; err != nil {
		return utils.NotFound(c, "user not found")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(input.CurrentPassword)); err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "current password is incorrect"})
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(input.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		return utils.InternalError(c, err)
	}

	user.Password = string(hash)
	if err := database.DB.Save(&user).Error; err != nil {
		return utils.InternalError(c, err)
	}

	services.WriteAudit(services.AuditInput{
		OrganizationID: &user.OrganizationID,
		ActorID:        &userID,
		ActorEmail:     middleware.CurrentEmail(c),
		Action:         models.AuditActionUpdate,
		Module:         models.AuditModuleSystem,
		ResourceID:     user.ID.String(),
		Description:    "Password changed.",
		IPAddress:      middleware.ClientIP(c),
		UserAgent:      c.Get("User-Agent"),
	})

	return utils.OK(c, fiber.Map{"message": "password updated successfully"})
}