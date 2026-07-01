package handlers

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/ledgefice/internal/database"
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

	// Check email not already taken
	var existing models.User
	if err := database.DB.Where("email = ?", input.Email).First(&existing).Error; err == nil {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "email already in use"})
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

	tx := database.DB.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 1. Create org (no owner yet)
	org := models.Organization{
		Name:            strings.TrimSpace(input.OrganizationName),
		LogoURL:         logoURL,
		NumberOfWorkers: input.NumberOfWorkers,
		Plan:            plan,
	}
	if err := tx.Create(&org).Error; err != nil {
		tx.Rollback()
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "organization name already exists"})
	}

	// 2. Create owner user — linked to org, no department yet
	owner := models.User{
		OrganizationID: org.ID,
		Name:           strings.TrimSpace(input.OrganizationName) + " Admin",
		Email:          strings.ToLower(strings.TrimSpace(input.Email)),
		Password:       string(hash),
		Status:         models.UserStatusActive,
	}
	if err := tx.Create(&owner).Error; err != nil {
		tx.Rollback()
		return utils.InternalError(c, err)
	}

	// 3. Link owner back to org
	org.OwnerID = &owner.ID
	if err := tx.Save(&org).Error; err != nil {
		tx.Rollback()
		return utils.InternalError(c, err)
	}

	// 4. Create owner department with full permissions
	ownerDept := models.Department{
		OrganizationID: org.ID,
		Name:           "Owner",
		Permissions:    models.FullPermissions(),
	}
	if err := tx.Create(&ownerDept).Error; err != nil {
		tx.Rollback()
		return utils.InternalError(c, err)
	}

	// 5. Assign owner to that department
	owner.DepartmentID = &ownerDept.ID
	if err := tx.Save(&owner).Error; err != nil {
		tx.Rollback()
		return utils.InternalError(c, err)
	}

	if err := tx.Commit().Error; err != nil {
		return utils.InternalError(c, err)
	}

	cfg := models.GetPlanConfig(plan)

	// Paid plans: create a Nomba checkout order with tokenizeCard so renewals
	// can charge the saved card later without re-collecting card details.
	var checkoutLink string
	if cfg.MonthlyPrice > 0 {
		orderRef := fmt.Sprintf("sub_%s_%d", org.ID.String()[:8], time.Now().Unix())
		amountNaira := float64(cfg.MonthlyPrice) / 100.0 // kobo -> naira

		result, err := h.Nomba.CreateCheckoutOrder(services.CheckoutOrderInput{
			OrderReference: orderRef,
			CustomerEmail:  owner.Email,
			Amount:         amountNaira,
			Currency:       "NGN",
			CallbackURL:    os.Getenv("APP_BASE_URL") + "/payments/nomba/callback",
			TokenizeCard:   true,
		})
		if err != nil {
			return utils.InternalError(c, err)
		}

		sub := models.Subscription{
			OrganizationID: org.ID,
			Plan:           plan,
			Amount:         amountNaira,
			Currency:       "NGN",
	        OrderReference: orderRef,
			CheckoutLink:   result.CheckoutLink,
			Status:         models.SubscriptionStatusPending,
		}
		if err := database.DB.Create(&sub).Error; err != nil {
			return utils.InternalError(c, err)
		}
		checkoutLink = result.CheckoutLink
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"message":       "workspace created",
		"checkout_link": checkoutLink,
		"org": fiber.Map{
			"id":   org.ID,
			"name": org.Name,
			"plan": org.Plan,
			"logo": org.LogoURL,
			"limits": fiber.Map{
				"max_departments": cfg.MaxDepartments,
				"max_users":       cfg.MaxUsers,
			},
			"features": cfg.Features,
		},
		"owner": fiber.Map{
			"id":    owner.ID,
			"email": owner.Email,
		},
	})
}