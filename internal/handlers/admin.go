package handlers

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/ledgefice/internal/database"
	"github.com/ledgefice/internal/middleware"
	"github.com/ledgefice/internal/models"
	"github.com/ledgefice/internal/services"
)

type AdminHandler struct {
	Renewal      *services.RenewalService
	JWTSecret    string
	JWTExpiresIn time.Duration
}

// ─── Auth ───────────────────────────────────────────────────────────────────

type adminLoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}


func (h *AdminHandler) Login(c *fiber.Ctx) error {
	var req adminLoginRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Email == "" || req.Password == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "email and password are required"})
	}

	var admin models.AdminUser
	if err := database.DB.Where("email = ?", req.Email).First(&admin).Error; err != nil {
	
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid email or password"})
	}

	if !admin.IsActive {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "admin account is disabled"})
	}

	if err := admin.CheckPassword(req.Password); err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid email or password"})
	}

	expiry := h.JWTExpiresIn
	if expiry == 0 {
		expiry = 24 * time.Hour
	}

	claims := middleware.AdminClaims{
		AdminID:   admin.ID,
		Email:     admin.Email,
		TokenType: "admin",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(expiry)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(h.JWTSecret))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to generate token"})
	}

	return c.JSON(fiber.Map{
		"token": signed,
		"admin": fiber.Map{
			"id":         admin.ID,
			"email":      admin.Email,
			"first_name": admin.FirstName,
			"last_name":  admin.LastName,
		},
	})
}

// ─── Renewal cron trigger ──────────────────────────────────────────────────

// RunRenewalsNow triggers ProcessDueRenewals immediately instead of waiting

func (h *AdminHandler) RunRenewalsNow(c *fiber.Ctx) error {
	if err := h.Renewal.ProcessDueRenewals(); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"message": "renewal pass complete — check server logs and subscription rows for results"})
}

// ─── Organizations ─────────────────────────────────────────────────────────

// ListOrganizations returns every organization on the platform, most recent

func (h *AdminHandler) ListOrganizations(c *fiber.Ctx) error {
	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 20)
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit

	var orgs []models.Organization
	var total int64

	database.DB.Model(&models.Organization{}).Count(&total)

	if err := database.DB.
		Order("created_at DESC").
		Limit(limit).
		Offset(offset).
		Find(&orgs).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list organizations"})
	}

	return c.JSON(fiber.Map{
		"organizations": orgs,
		"total":         total,
		"page":          page,
		"limit":         limit,
	})
}

// GetOrganization returns one org plus its subscription history, since as an
// admin you'll almost always want billing context alongside org details.
func (h *AdminHandler) GetOrganization(c *fiber.Ctx) error {
	id := c.Params("id")

	var org models.Organization
	if err := database.DB.Where("id = ?", id).First(&org).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "organization not found"})
	}

	var subs []models.Subscription
	database.DB.Where("organization_id = ?", id).Order("created_at DESC").Find(&subs)

	var userCount int64
	database.DB.Model(&models.User{}).Where("organization_id = ?", id).Count(&userCount)

	return c.JSON(fiber.Map{
		"organization":  org,
		"subscriptions": subs,
		"user_count":    userCount,
	})
}

type updateOrgRequest struct {
	Name            *string          `json:"name"`
	LogoURL         *string          `json:"logo_url"`
	NumberOfWorkers *int             `json:"number_of_workers"`
	Plan            *models.PlanType `json:"plan"`
}


func (h *AdminHandler) UpdateOrganization(c *fiber.Ctx) error {
	id := c.Params("id")

	var org models.Organization
	if err := database.DB.Where("id = ?", id).First(&org).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "organization not found"})
	}

	var req updateOrgRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	if req.Name != nil {
		org.Name = *req.Name
	}
	if req.LogoURL != nil {
		org.LogoURL = *req.LogoURL
	}
	if req.NumberOfWorkers != nil {
		org.NumberOfWorkers = *req.NumberOfWorkers
	}
	if req.Plan != nil {
		org.Plan = *req.Plan
	}

	if err := database.DB.Save(&org).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update organization"})
	}

	return c.JSON(fiber.Map{"organization": org})
}

// DeleteOrganization soft-deletes the org 
func (h *AdminHandler) DeleteOrganization(c *fiber.Ctx) error {
	id := c.Params("id")

	var org models.Organization
	if err := database.DB.Where("id = ?", id).First(&org).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "organization not found"})
	}

	if err := database.DB.Delete(&org).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to delete organization"})
	}

	return c.JSON(fiber.Map{"message": "organization deleted"})
}