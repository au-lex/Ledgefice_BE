package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/ledgefice/internal/database"
	"github.com/ledgefice/internal/middleware"
	"github.com/ledgefice/internal/models"
	"github.com/ledgefice/internal/services"
	"github.com/ledgefice/pkg/utils"
)

type VoucherTypeHandler struct{}

func (h *VoucherTypeHandler) List(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)

	var types []models.VoucherType
	database.DB.Preload("CustomFields").Where("organization_id = ?", orgID).Find(&types)
	return utils.OK(c, types)
}

func (h *VoucherTypeHandler) Get(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return utils.BadRequest(c, "invalid id")
	}
	var vt models.VoucherType
	if err := database.DB.Preload("CustomFields").
		First(&vt, "id = ? AND organization_id = ?", id, orgID).Error; err != nil {
		return utils.NotFound(c, "voucher type not found")
	}
	return utils.OK(c, vt)
}

type customFieldInput struct {
	Label     string           `json:"label"`
	FieldType models.FieldType `json:"type"`
	SortOrder int              `json:"sort_order"`
}

type voucherTypeInput struct {
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Fields      []customFieldInput `json:"fields"`
}

func (h *VoucherTypeHandler) Create(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)

	var input voucherTypeInput
	if err := c.BodyParser(&input); err != nil || input.Name == "" {
		return utils.BadRequest(c, "name is required")
	}

	vt := models.VoucherType{
		OrganizationID: orgID,
		Name:           input.Name,
		Description:    input.Description,
	}
	for i, f := range input.Fields {
		vt.CustomFields = append(vt.CustomFields, models.CustomField{
			Label:     f.Label,
			FieldType: f.FieldType,
			SortOrder: i,
		})
	}

	if err := database.DB.Create(&vt).Error; err != nil {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "name already exists"})
	}

	actorID := middleware.CurrentUserID(c)
	services.WriteAudit(services.AuditInput{
		OrganizationID: &orgID,
		ActorID:        &actorID,
		ActorEmail:     middleware.CurrentEmail(c),
		Action:         models.AuditActionCreate,
		Module:         models.AuditModuleWorkflows,
		ResourceID:     vt.ID.String(),
		Description:    "Created voucher type '" + vt.Name + "'.",
		IPAddress:      middleware.ClientIP(c),
		UserAgent:      c.Get("User-Agent"),
	})

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"data": vt})
}

func (h *VoucherTypeHandler) Update(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return utils.BadRequest(c, "invalid id")
	}
	var vt models.VoucherType
	if err := database.DB.First(&vt, "id = ? AND organization_id = ?", id, orgID).Error; err != nil {
		return utils.NotFound(c, "voucher type not found")
	}

	var input voucherTypeInput
	c.BodyParser(&input)

	if input.Name != "" {
		vt.Name = input.Name
	}
	if input.Description != "" {
		vt.Description = input.Description
	}
	if input.Fields != nil {
		database.DB.Where("voucher_type_id = ?", vt.ID).Delete(&models.CustomField{})
		for i, f := range input.Fields {
			database.DB.Create(&models.CustomField{
				VoucherTypeID: vt.ID,
				Label:         f.Label,
				FieldType:     f.FieldType,
				SortOrder:     i,
			})
		}
	}
	database.DB.Save(&vt)
	database.DB.Preload("CustomFields").First(&vt, "id = ?", id)

	return utils.OK(c, vt)
}

func (h *VoucherTypeHandler) Delete(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return utils.BadRequest(c, "invalid id")
	}
	var vt models.VoucherType
	if err := database.DB.First(&vt, "id = ? AND organization_id = ?", id, orgID).Error; err != nil {
		return utils.NotFound(c, "voucher type not found")
	}
	database.DB.Delete(&vt)
	return c.SendStatus(fiber.StatusNoContent)
}