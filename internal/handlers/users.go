package handlers

import (
	"crypto/rand"
	"math/big"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/ledgefice/internal/database"
	"github.com/ledgefice/internal/middleware"
	"github.com/ledgefice/internal/models"
	"github.com/ledgefice/internal/services"
	"github.com/ledgefice/pkg/utils"
	"golang.org/x/crypto/bcrypt"
)

type UserHandler struct {
	Email *services.EmailClient
}

func generatePassword() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$"
	b := make([]byte, 12)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		b[i] = charset[n.Int64()]
	}
	return string(b)
}

type createUserInput struct {
	Name         string `json:"name"`
	Email        string `json:"email"`
	DepartmentID string `json:"department_id"`
}

func (h *UserHandler) Create(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)

	var input createUserInput
	if err := c.BodyParser(&input); err != nil {
		return utils.BadRequest(c, "invalid request body")
	}
	if input.Name == "" || input.Email == "" || input.DepartmentID == "" {
		return utils.BadRequest(c, "name, email, and department_id are required")
	}

	deptID, err := uuid.Parse(input.DepartmentID)
	if err != nil {
		return utils.BadRequest(c, "invalid department_id")
	}

	// Department must belong to this org
	var dept models.Department
	if err := database.DB.First(&dept, "id = ? AND organization_id = ?", deptID, orgID).Error; err != nil {
		return utils.NotFound(c, "department not found")
	}

	// Plan limit check
	var org models.Organization
	database.DB.First(&org, "id = ?", orgID)
	cfg := models.GetPlanConfig(org.Plan)
	if cfg.MaxUsers > 0 {
		var userCount int64
		database.DB.Model(&models.User{}).Where("organization_id = ?", orgID).Count(&userCount)
		if int(userCount) >= cfg.MaxUsers {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "user limit reached for your plan",
			})
		}
	}

	plainPassword := generatePassword()
	hash, err := bcrypt.GenerateFromPassword([]byte(plainPassword), bcrypt.DefaultCost)
	if err != nil {
		return utils.InternalError(c, err)
	}

	user := models.User{
		OrganizationID: orgID,
		Name:           strings.TrimSpace(input.Name),
		Email:          strings.ToLower(strings.TrimSpace(input.Email)),
		Password:       string(hash),
		DepartmentID:   &deptID,
		Status:         models.UserStatusActive,
	}
	if err := database.DB.Create(&user).Error; err != nil {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "email already in use"})
	}

	go func() {
		_ = h.Email.SendWelcome(user.Email, user.Name, plainPassword, org.Name, dept.Name)
	}()

	actorID := middleware.CurrentUserID(c)
	services.WriteAudit(services.AuditInput{
		OrganizationID: &orgID,
		ActorID:        &actorID,
		ActorEmail:     middleware.CurrentEmail(c),
		Action:         models.AuditActionCreate,
		Module:         models.AuditModuleUsers,
		ResourceID:     user.ID.String(),
		Description:    "Created user account for " + user.Name + " in " + dept.Name,
		IPAddress:      middleware.ClientIP(c),
		UserAgent:      c.Get("User-Agent"),
	})

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"message": "user created and invite sent",
		"user": fiber.Map{
			"id":         user.ID,
			"name":       user.Name,
			"email":      user.Email,
			"department": dept.Name,
			"status":     user.Status,
		},
	})
}

func (h *UserHandler) List(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)
	pg := utils.ParsePagination(c)

	status := c.Query("status")
	department := c.Query("department")
	search := c.Query("search")

	query := database.DB.Preload("Department").Model(&models.User{}).
		Where("users.organization_id = ?", orgID)

	if status != "" {
		query = query.Where("users.status = ?", status)
	}
	if department != "" {
		query = query.Joins("JOIN departments ON departments.id = users.department_id").
			Where("departments.name = ?", department)
	}
	if search != "" {
		query = query.Where("users.name ILIKE ? OR users.email ILIKE ?", "%"+search+"%", "%"+search+"%")
	}

	var total int64
	query.Count(&total)

	var users []models.User
	query.Offset(pg.Offset).Limit(pg.Limit).Find(&users)

	return c.JSON(utils.Paginated(users, total, pg))
}

func (h *UserHandler) Get(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return utils.BadRequest(c, "invalid id")
	}
	var user models.User
	if err := database.DB.Preload("Department").
		First(&user, "id = ? AND organization_id = ?", id, orgID).Error; err != nil {
		return utils.NotFound(c, "user not found")
	}
	return utils.OK(c, user)
}

type updateUserInput struct {
	Name         string `json:"name"`
	Email        string `json:"email"`
	DepartmentID string `json:"department_id"`
	Password     string `json:"password"`
}

func (h *UserHandler) Update(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return utils.BadRequest(c, "invalid id")
	}
	var user models.User
	if err := database.DB.First(&user, "id = ? AND organization_id = ?", id, orgID).Error; err != nil {
		return utils.NotFound(c, "user not found")
	}

	var input updateUserInput
	c.BodyParser(&input)

	if input.Name != "" {
		user.Name = input.Name
	}
	if input.Email != "" {
		user.Email = input.Email
	}
	if input.DepartmentID != "" {
		deptID, err := uuid.Parse(input.DepartmentID)
		if err == nil {
			// Ensure the target department belongs to this org
			var dept models.Department
			if database.DB.First(&dept, "id = ? AND organization_id = ?", deptID, orgID).Error == nil {
				user.DepartmentID = &deptID
			}
		}
	}
	if input.Password != "" {
		hash, _ := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
		user.Password = string(hash)
	}

	database.DB.Save(&user)

	actorID := middleware.CurrentUserID(c)
	services.WriteAudit(services.AuditInput{
		OrganizationID: &orgID,
		ActorID:        &actorID,
		ActorEmail:     middleware.CurrentEmail(c),
		Action:         models.AuditActionUpdate,
		Module:         models.AuditModuleUsers,
		ResourceID:     user.ID.String(),
		Description:    "Updated user profile for " + user.Name,
		IPAddress:      middleware.ClientIP(c),
		UserAgent:      c.Get("User-Agent"),
	})

	return utils.OK(c, user)
}

func (h *UserHandler) Delete(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return utils.BadRequest(c, "invalid id")
	}
	var user models.User
	if err := database.DB.First(&user, "id = ? AND organization_id = ?", id, orgID).Error; err != nil {
		return utils.NotFound(c, "user not found")
	}
	database.DB.Delete(&user)

	actorID := middleware.CurrentUserID(c)
	services.WriteAudit(services.AuditInput{
		OrganizationID: &orgID,
		ActorID:        &actorID,
		ActorEmail:     middleware.CurrentEmail(c),
		Action:         models.AuditActionDelete,
		Module:         models.AuditModuleUsers,
		ResourceID:     id.String(),
		Description:    "Permanently deleted user account for " + user.Name,
		IPAddress:      middleware.ClientIP(c),
		UserAgent:      c.Get("User-Agent"),
	})

	return c.SendStatus(fiber.StatusNoContent)
}

type setStatusInput struct {
	Status models.UserStatus `json:"status"`
}

func (h *UserHandler) SetStatus(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return utils.BadRequest(c, "invalid id")
	}
	var input setStatusInput
	if err := c.BodyParser(&input); err != nil || input.Status == "" {
		return utils.BadRequest(c, "status is required")
	}

	var user models.User
	if err := database.DB.First(&user, "id = ? AND organization_id = ?", id, orgID).Error; err != nil {
		return utils.NotFound(c, "user not found")
	}
	user.Status = input.Status
	database.DB.Save(&user)

	return utils.OK(c, fiber.Map{"id": user.ID, "status": user.Status})
}