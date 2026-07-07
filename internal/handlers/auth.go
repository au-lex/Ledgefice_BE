package handlers

import (
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

type AuthHandler struct {
	JWTSecret    string
	JWTExpiresIn string
	Images       *services.ImageService
}

type loginInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *AuthHandler) Login(c *fiber.Ctx) error {
	var input loginInput
	if err := c.BodyParser(&input); err != nil {
		return utils.BadRequest(c, "invalid request body")
	}

	var user models.User
	if err := database.DB.Preload("Department").Where("email = ?", input.Email).First(&user).Error; err != nil {
		services.WriteAudit(services.AuditInput{
			ActorEmail:  input.Email,
			ActorName:   "Unknown",
			Action:      models.AuditActionAuthFailure,
			Module:      models.AuditModuleSystem,
			ResourceID:  "AUTH",
			Description: "Failed login attempt for " + input.Email + " (user not found).",
			IPAddress:   middleware.ClientIP(c),
			UserAgent:   c.Get("User-Agent"),
		})
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid credentials"})
	}

	if user.Status != models.UserStatusActive {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "account is " + string(user.Status)})
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(input.Password)); err != nil {
		orgID := user.OrganizationID
		services.WriteAudit(services.AuditInput{
			OrganizationID: &orgID,
			ActorID:        &user.ID,
			ActorEmail:     user.Email,
			ActorName:      user.Name,
			Action:         models.AuditActionAuthFailure,
			Module:         models.AuditModuleSystem,
			ResourceID:     "AUTH",
			Description:    "Failed login attempt for " + user.Email + " (wrong password).",
			IPAddress:      middleware.ClientIP(c),
			UserAgent:      c.Get("User-Agent"),
		})
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid credentials"})
	}

	// ── Org lookup: direct, no fallback ──────────────────────────────────────
	var org models.Organization
	if err := database.DB.First(&org, "id = ?", user.OrganizationID).Error; err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "organization not found"})
	}
	planCfg := models.GetPlanConfig(org.Plan)

	deptName := ""
	if user.Department != nil {
		deptName = user.Department.Name
	}

	token, err := utils.GenerateToken(user.ID, user.Email, org.ID, deptName, h.JWTSecret, h.JWTExpiresIn)
	if err != nil {
		return utils.InternalError(c, err)
	}

	now := time.Now()
	database.DB.Model(&user).Update("last_login_at", now)

	orgID := user.OrganizationID
	services.WriteAudit(services.AuditInput{
		OrganizationID: &orgID,
		ActorID:        &user.ID,
		ActorEmail:     user.Email,
		ActorName:      user.Name,
		Action:         models.AuditActionAuthSuccess,
		Module:         models.AuditModuleSystem,
		ResourceID:     "AUTH",
		Description:    "Successful login.",
		IPAddress:      middleware.ClientIP(c),
		UserAgent:      c.Get("User-Agent"),
	})

	perms := models.PermissionMap{}
	if user.Department != nil {
		perms = user.Department.Permissions
	}

	return c.JSON(fiber.Map{
		"token": token,
		"user": fiber.Map{
			"id":          user.ID,
			"name":        user.Name,
			"email":       user.Email,
			"department":  deptName,
			"status":      user.Status,
			"avatar_url":  user.AvatarURL,
			"permissions": perms,
		},
		"org": fiber.Map{
			"id":   org.ID,
			"name": org.Name,
			"plan": org.Plan,
			"logo": org.LogoURL,
			"limits": fiber.Map{
				"max_departments": planCfg.MaxDepartments,
				"max_users":       planCfg.MaxUsers,
			},
			"features": planCfg.Features,
		},
	})
}

func (h *AuthHandler) Me(c *fiber.Ctx) error {
	userID := middleware.CurrentUserID(c)

	var user models.User
	if err := database.DB.Preload("Department").First(&user, "id = ?", userID).Error; err != nil {
		return utils.NotFound(c, "user not found")
	}

	// Direct org lookup via the stored OrganizationID — no guessing
	var org models.Organization
	if err := database.DB.First(&org, "id = ?", user.OrganizationID).Error; err != nil {
		return utils.NotFound(c, "organization not found")
	}
	planCfg := models.GetPlanConfig(org.Plan)

	perms := models.PermissionMap{}
	if user.Department != nil {
		perms = user.Department.Permissions
	}

	return utils.OK(c, fiber.Map{
		"id":          user.ID,
		"name":        user.Name,
		"email":       user.Email,
		"department":  user.Department,
		"status":      user.Status,
		"avatar_url":  user.AvatarURL,
		"permissions": perms,
		"org": fiber.Map{
			"id":   org.ID,
			"name": org.Name,
			"plan": org.Plan,
			"logo": org.LogoURL,
			"limits": fiber.Map{
				"max_departments": planCfg.MaxDepartments,
				"max_users":       planCfg.MaxUsers,
			},
			"features": planCfg.Features,
		},
	})
}



func (h *AuthHandler) UpdateMe(c *fiber.Ctx) error {
	userID := middleware.CurrentUserID(c)

	var user models.User
	if err := database.DB.Preload("Department").First(&user, "id = ?", userID).Error; err != nil {
		return utils.NotFound(c, "user not found")
	}

	name := strings.TrimSpace(c.FormValue("name"))
	email := strings.TrimSpace(c.FormValue("email"))
	currentPassword := c.FormValue("current_password")
	newPassword := c.FormValue("new_password")

	changed := []string{}

	if name != "" && name != user.Name {
		user.Name = name
		changed = append(changed, "name")
	}

	if email != "" {
		newEmail := strings.ToLower(email)
		if newEmail != user.Email {
			var existing models.User
			if err := database.DB.Where("email = ? AND id != ?", newEmail, user.ID).First(&existing).Error; err == nil {
				return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "email already in use"})
			}
			user.Email = newEmail
			changed = append(changed, "email")
		}
	}

	if newPassword != "" {
		if currentPassword == "" {
			return utils.BadRequest(c, "current_password is required to set a new password")
		}
		if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(currentPassword)); err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "current password is incorrect"})
		}
		if len(newPassword) < 8 {
			return utils.BadRequest(c, "new password must be at least 8 characters")
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
		if err != nil {
			return utils.InternalError(c, err)
		}
		user.Password = string(hash)
		changed = append(changed, "password")
	}

	// ── Avatar upload (optional) ──────────────────────────────────────────
	fileHeader, ferr := c.FormFile("avatar")
	if ferr == nil && fileHeader != nil {
		if h.Images == nil {
			return utils.InternalError(c, fiber.NewError(fiber.StatusInternalServerError, "image service not configured"))
		}
		f, err := fileHeader.Open()
		if err != nil {
			return utils.BadRequest(c, "could not open uploaded file")
		}

		secureURL, publicID, err := h.Images.Upload(f, fileHeader, "avatars")
		if err != nil {
			return utils.InternalError(c, err)
		}

		oldPublicID := user.AvatarPublicID
		user.AvatarURL = secureURL
		user.AvatarPublicID = publicID
		changed = append(changed, "avatar")

		if oldPublicID != "" {
			go func(id string) {
				_ = h.Images.Delete(id)
			}(oldPublicID)
		}
	}

	if len(changed) == 0 {
		return utils.BadRequest(c, "no changes provided")
	}

	if err := database.DB.Save(&user).Error; err != nil {
		return utils.InternalError(c, err)
	}

	orgID := user.OrganizationID
	services.WriteAudit(services.AuditInput{
		OrganizationID: &orgID,
		ActorID:        &user.ID,
		ActorEmail:     user.Email,
		ActorName:      user.Name,
		Action:         models.AuditActionUpdate,
		Module:         models.AuditModuleUsers,
		ResourceID:     user.ID.String(),
		Description:    "User updated own profile (" + strings.Join(changed, ", ") + ").",
		IPAddress:      middleware.ClientIP(c),
		UserAgent:      c.Get("User-Agent"),
	})

	deptName := ""
	if user.Department != nil {
		deptName = user.Department.Name
	}
	perms := models.PermissionMap{}
	if user.Department != nil {
		perms = user.Department.Permissions
	}

	return utils.OK(c, fiber.Map{
		"id":          user.ID,
		"name":        user.Name,
		"email":       user.Email,
		"department":  deptName,
		"status":      user.Status,
		"avatar_url":  user.AvatarURL,
		"permissions": perms,
	})
}