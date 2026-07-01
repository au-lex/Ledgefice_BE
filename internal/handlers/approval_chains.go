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

type ApprovalChainHandler struct{}

func (h *ApprovalChainHandler) List(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)

	var chains []models.ApprovalChain
	database.DB.
		Preload("VoucherType").
		Preload("Tiers.Steps.Department").
		Where("organization_id = ?", orgID).
		Find(&chains)
	return utils.OK(c, chains)
}

func (h *ApprovalChainHandler) Get(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return utils.BadRequest(c, "invalid id")
	}
	var chain models.ApprovalChain
	if err := database.DB.
		Preload("VoucherType").
		Preload("Tiers.Steps.Department").
		First(&chain, "id = ? AND organization_id = ?", id, orgID).Error; err != nil {
		return utils.NotFound(c, "approval chain not found")
	}
	return utils.OK(c, chain)
}

type approverStepInput struct {
	DepartmentID string `json:"department_id"`
	StepOrder    int    `json:"step_order"`
}

type amountTierInput struct {
	Label     string             `json:"label"`
	MinAmount float64            `json:"min_amount"`
	MaxAmount *float64           `json:"max_amount"`
	SortOrder int                `json:"sort_order"`
	Steps     []approverStepInput `json:"steps"`
}

type approvalChainInput struct {
	VoucherTypeID string            `json:"voucher_type_id"`
	Tiers         []amountTierInput `json:"tiers"`
}

func validateStepDepts(input approvalChainInput, orgID uuid.UUID) error {
	for _, t := range input.Tiers {
		for _, s := range t.Steps {
			deptID, err := uuid.Parse(s.DepartmentID)
			if err != nil {
				return fiber.NewError(fiber.StatusBadRequest, "invalid department_id in steps")
			}
			var dept models.Department
			if err := database.DB.First(&dept, "id = ? AND organization_id = ?", deptID, orgID).Error; err != nil {
				return fiber.NewError(fiber.StatusNotFound, "department not found: "+s.DepartmentID)
			}
		}
	}
	return nil
}

func (h *ApprovalChainHandler) Create(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)

	var input approvalChainInput
	if err := c.BodyParser(&input); err != nil || input.VoucherTypeID == "" {
		return utils.BadRequest(c, "voucher_type_id is required")
	}

	vtID, err := uuid.Parse(input.VoucherTypeID)
	if err != nil {
		return utils.BadRequest(c, "invalid voucher_type_id")
	}

	var vt models.VoucherType
	if err := database.DB.First(&vt, "id = ? AND organization_id = ?", vtID, orgID).Error; err != nil {
		return utils.NotFound(c, "voucher type not found")
	}

	if err := validateStepDepts(input, orgID); err != nil {
		return err
	}

	chain := models.ApprovalChain{
		OrganizationID: orgID,
		VoucherTypeID:  vtID,
	}
	for i, t := range input.Tiers {
		tier := models.AmountTier{
			Label:     t.Label,
			MinAmount: t.MinAmount,
			MaxAmount: t.MaxAmount,
			SortOrder: i,
		}
		for j, s := range t.Steps {
			deptID, _ := uuid.Parse(s.DepartmentID)
			tier.Steps = append(tier.Steps, models.ApproverStep{
				DepartmentID: deptID,
				StepOrder:    j,
			})
		}
		chain.Tiers = append(chain.Tiers, tier)
	}

	if err := database.DB.Create(&chain).Error; err != nil {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "chain already exists for this voucher type"})
	}

	actorID := middleware.CurrentUserID(c)
	services.WriteAudit(services.AuditInput{
		OrganizationID: &orgID,
		ActorID:        &actorID,
		ActorEmail:     middleware.CurrentEmail(c),
		Action:         models.AuditActionCreate,
		Module:         models.AuditModuleWorkflows,
		ResourceID:     chain.ID.String(),
		Description:    "Created approval chain for voucher type " + vt.Name,
		IPAddress:      middleware.ClientIP(c),
		UserAgent:      c.Get("User-Agent"),
	})

	database.DB.Preload("VoucherType").Preload("Tiers.Steps.Department").First(&chain, "id = ?", chain.ID)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"data": chain})
}

func (h *ApprovalChainHandler) Update(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return utils.BadRequest(c, "invalid id")
	}
	var chain models.ApprovalChain
	if err := database.DB.First(&chain, "id = ? AND organization_id = ?", id, orgID).Error; err != nil {
		return utils.NotFound(c, "approval chain not found")
	}

	var input approvalChainInput
	c.BodyParser(&input)

	if err := validateStepDepts(input, orgID); err != nil {
		return err
	}

	database.DB.Where("approval_chain_id = ?", chain.ID).Delete(&models.AmountTier{})

	for i, t := range input.Tiers {
		tier := models.AmountTier{
			ApprovalChainID: chain.ID,
			Label:           t.Label,
			MinAmount:       t.MinAmount,
			MaxAmount:       t.MaxAmount,
			SortOrder:       i,
		}
		database.DB.Create(&tier)
		for j, s := range t.Steps {
			deptID, _ := uuid.Parse(s.DepartmentID)
			database.DB.Create(&models.ApproverStep{
				AmountTierID: tier.ID,
				DepartmentID: deptID,
				StepOrder:    j,
			})
		}
	}

	actorID := middleware.CurrentUserID(c)
	services.WriteAudit(services.AuditInput{
		OrganizationID: &orgID,
		ActorID:        &actorID,
		ActorEmail:     middleware.CurrentEmail(c),
		Action:         models.AuditActionUpdate,
		Module:         models.AuditModuleWorkflows,
		ResourceID:     chain.ID.String(),
		Description:    "Modified approval chain configuration.",
		IPAddress:      middleware.ClientIP(c),
		UserAgent:      c.Get("User-Agent"),
	})

	database.DB.Preload("VoucherType").Preload("Tiers.Steps.Department").First(&chain, "id = ?", id)
	return utils.OK(c, chain)
}

func (h *ApprovalChainHandler) Delete(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return utils.BadRequest(c, "invalid id")
	}
	var chain models.ApprovalChain
	if err := database.DB.First(&chain, "id = ? AND organization_id = ?", id, orgID).Error; err != nil {
		return utils.NotFound(c, "approval chain not found")
	}
	database.DB.Delete(&chain)
	return c.SendStatus(fiber.StatusNoContent)
}