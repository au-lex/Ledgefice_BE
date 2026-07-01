package handlers

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/ledgefice/internal/database"
	"github.com/ledgefice/internal/middleware"
	"github.com/ledgefice/internal/models"
	"github.com/ledgefice/internal/services"
	"github.com/ledgefice/pkg/utils"
)

type DepartmentHandler struct{}

func (h *DepartmentHandler) List(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)

	var depts []models.Department
	database.DB.Preload("Users").Where("organization_id = ?", orgID).Find(&depts)

	type deptWithStats struct {
		models.Department
		ActiveVouchers int64   `json:"active_vouchers"`
		TotalSpend     float64 `json:"total_spend"`
		HeadCount      int     `json:"head_count"`
	}

	result := make([]deptWithStats, 0, len(depts))
	for _, d := range depts {
		var activeCount int64
		database.DB.Model(&models.Voucher{}).
			Where("department_id = ? AND organization_id = ? AND status IN ?",
				d.ID, orgID, []string{"pending", "approved"}).
			Count(&activeCount)

		var totalSpend float64
		database.DB.
			Table("voucher_field_values").
			Joins("JOIN custom_fields ON custom_fields.id = voucher_field_values.custom_field_id").
			Joins("JOIN vouchers ON vouchers.id = voucher_field_values.voucher_id").
			Where("vouchers.department_id = ? AND vouchers.organization_id = ? AND vouchers.status = ? AND custom_fields.field_type = ?",
				d.ID, orgID, models.VoucherStatusApproved, models.FieldTypeNumber).
			Select("COALESCE(SUM(CAST(voucher_field_values.value AS NUMERIC)), 0)").
			Scan(&totalSpend)

		result = append(result, deptWithStats{
			Department:     d,
			ActiveVouchers: activeCount,
			TotalSpend:     totalSpend,
			HeadCount:      len(d.Users),
		})
	}

	return utils.OK(c, result)
}

func (h *DepartmentHandler) Get(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return utils.BadRequest(c, "invalid id")
	}
	var dept models.Department
	if err := database.DB.Preload("Users").
		First(&dept, "id = ? AND organization_id = ?", id, orgID).Error; err != nil {
		return utils.NotFound(c, "department not found")
	}
	return utils.OK(c, dept)
}

type deptInput struct {
	Name        string          `json:"name"`
	Code        string          `json:"code"`
	IconKey     string          `json:"icon_key"`
	Permissions map[string]bool `json:"permissions"`
}

func (h *DepartmentHandler) Create(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)

	var input deptInput
	if err := c.BodyParser(&input); err != nil || input.Name == "" || input.Code == "" {
		return utils.BadRequest(c, "name and code are required")
	}

	var org models.Organization
	database.DB.First(&org, "id = ?", orgID)
	cfg := models.GetPlanConfig(org.Plan)
	if cfg.MaxDepartments > 0 {
		var deptCount int64
		database.DB.Model(&models.Department{}).Where("organization_id = ?", orgID).Count(&deptCount)
		if int(deptCount) >= cfg.MaxDepartments {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "department limit reached for your plan",
			})
		}
	}

	dept := models.Department{
		OrganizationID: orgID,
		Name:           input.Name,
		Code:           strings.ToUpper(input.Code),
		IconKey:        input.IconKey,
		Permissions:    input.Permissions,
	}
	if dept.IconKey == "" {
		dept.IconKey = "Building"
	}
	if dept.Permissions == nil {
		dept.Permissions = models.PermissionMap{}
	}

	if err := database.DB.Create(&dept).Error; err != nil {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "name or code already exists in this organization"})
	}

	actorID := middleware.CurrentUserID(c)
	services.WriteAudit(services.AuditInput{
		OrganizationID: &orgID,
		ActorID:        &actorID,
		ActorEmail:     middleware.CurrentEmail(c),
		Action:         models.AuditActionCreate,
		Module:         models.AuditModuleDepartments,
		ResourceID:     dept.Code,
		Description:    "Created new department '" + dept.Name + "'.",
		IPAddress:      middleware.ClientIP(c),
		UserAgent:      c.Get("User-Agent"),
	})

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"data": dept})
}

func (h *DepartmentHandler) Update(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return utils.BadRequest(c, "invalid id")
	}
	var dept models.Department
	if err := database.DB.First(&dept, "id = ? AND organization_id = ?", id, orgID).Error; err != nil {
		return utils.NotFound(c, "department not found")
	}

	var input deptInput
	c.BodyParser(&input)

	if input.Name != "" {
		dept.Name = input.Name
	}
	if input.Code != "" {
		dept.Code = strings.ToUpper(input.Code)
	}
	if input.IconKey != "" {
		dept.IconKey = input.IconKey
	}
	if input.Permissions != nil {
		dept.Permissions = input.Permissions
	}

	database.DB.Save(&dept)

	actorID := middleware.CurrentUserID(c)
	services.WriteAudit(services.AuditInput{
		OrganizationID: &orgID,
		ActorID:        &actorID,
		ActorEmail:     middleware.CurrentEmail(c),
		Action:         models.AuditActionUpdate,
		Module:         models.AuditModuleDepartments,
		ResourceID:     dept.Code,
		Description:    "Updated department '" + dept.Name + "'.",
		IPAddress:      middleware.ClientIP(c),
		UserAgent:      c.Get("User-Agent"),
	})

	return utils.OK(c, dept)
}

func (h *DepartmentHandler) Delete(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return utils.BadRequest(c, "invalid id")
	}
	var dept models.Department
	if err := database.DB.First(&dept, "id = ? AND organization_id = ?", id, orgID).Error; err != nil {
		return utils.NotFound(c, "department not found")
	}
	database.DB.Delete(&dept)

	actorID := middleware.CurrentUserID(c)
	services.WriteAudit(services.AuditInput{
		OrganizationID: &orgID,
		ActorID:        &actorID,
		ActorEmail:     middleware.CurrentEmail(c),
		Action:         models.AuditActionDelete,
		Module:         models.AuditModuleDepartments,
		ResourceID:     dept.Code,
		Description:    "Deleted department '" + dept.Name + "'.",
		IPAddress:      middleware.ClientIP(c),
		UserAgent:      c.Get("User-Agent"),
	})

	return c.SendStatus(fiber.StatusNoContent)
}