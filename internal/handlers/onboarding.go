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

// Setup NEVER creates an Organization/User/Department directly — every plan
// requires payment, so this only ever creates a PendingSignup + Nomba
// checkout order. The actual account gets created by PaymentHandler's webhook
// once payment_success is confirmed 
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