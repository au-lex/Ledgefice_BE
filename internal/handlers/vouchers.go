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

type VoucherHandler struct{}

func (h *VoucherHandler) List(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)
	pg := utils.ParsePagination(c)

	status := c.Query("status")
	vType := c.Query("type")
	dept := c.Query("department")
	search := c.Query("search")
	sort := c.Query("sort", "newest")

	query := database.DB.
		Preload("Department", models.SlimDept).
		Preload("VoucherType").
		Preload("RaisedBy", models.SlimUser).
		Preload("DuplicateFlag").
		Preload("ApprovalHistory.Actor", models.SlimUser).
		Preload("ApprovalHistory.Department", models.SlimDept).
		Preload("FieldValues.CustomField").
		Preload("CurrentApproverDept", models.SlimDept).
		Preload("AmountTier.Steps.Department", models.SlimDept).
		Model(&models.Voucher{}).
		Where("vouchers.organization_id = ?", orgID)

	if status != "" {
		query = query.Where("vouchers.status = ?", status)
	}
	if vType != "" {
		query = query.Joins("JOIN voucher_types ON voucher_types.id = vouchers.voucher_type_id").
			Where("voucher_types.name = ?", vType)
	}
	if dept != "" {
		query = query.Joins("JOIN departments ON departments.id = vouchers.department_id").
			Where("departments.name = ?", dept)
	}
	if search != "" {
		query = query.Where("vouchers.code ILIKE ?", "%"+search+"%")
	}

	switch sort {
	case "oldest":
		query = query.Order("vouchers.created_at ASC")
	default:
		query = query.Order("vouchers.created_at DESC")
	}

	var total int64
	query.Count(&total)

	var vouchers []models.Voucher
	query.Offset(pg.Offset).Limit(pg.Limit).Find(&vouchers)

	return c.JSON(utils.Paginated(vouchers, total, pg))
}

func (h *VoucherHandler) Get(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return utils.BadRequest(c, "invalid id")
	}
	var v models.Voucher
	if err := database.DB.
		Preload("Department", models.SlimDept).
		Preload("VoucherType.CustomFields").
		Preload("RaisedBy", models.SlimUser).
		Preload("FieldValues.CustomField").
		Preload("ApprovalHistory.Actor", models.SlimUser).
		Preload("ApprovalHistory.Department", models.SlimDept).
		Preload("DuplicateFlag").
		Preload("CurrentApproverDept", models.SlimDept).
		Preload("AmountTier.Steps.Department", models.SlimDept).
		First(&v, "id = ? AND organization_id = ?", id, orgID).Error; err != nil {
		return utils.NotFound(c, "voucher not found")
	}
	return utils.OK(c, v)
}

type fieldValueInput struct {
	CustomFieldID string `json:"custom_field_id"`
	Value         string `json:"value"`
}

type createVoucherInput struct {
	VoucherTypeID string            `json:"voucher_type_id"`
	FieldValues   []fieldValueInput `json:"field_values"`
}

func (h *VoucherHandler) Create(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)
	actorID := middleware.CurrentUserID(c)

	var input createVoucherInput
	if err := c.BodyParser(&input); err != nil {
		return utils.BadRequest(c, "invalid request body")
	}
	if input.VoucherTypeID == "" {
		return utils.BadRequest(c, "voucher_type_id is required")
	}

	vtID, err := uuid.Parse(input.VoucherTypeID)
	if err != nil {
		return utils.BadRequest(c, "invalid voucher_type_id")
	}

	var actor models.User
	if err := database.DB.First(&actor, "id = ? AND organization_id = ?", actorID, orgID).Error; err != nil {
		return utils.NotFound(c, "user not found")
	}
	if actor.DepartmentID == nil {
		return utils.BadRequest(c, "you are not assigned to a department")
	}

	var vt models.VoucherType
	if err := database.DB.Preload("CustomFields").
		First(&vt, "id = ? AND organization_id = ?", vtID, orgID).Error; err != nil {
		return utils.NotFound(c, "voucher type not found")
	}

	provided := map[string]bool{}
	for _, fv := range input.FieldValues {
		provided[fv.CustomFieldID] = true
	}
	for _, cf := range vt.CustomFields {
		if !provided[cf.ID.String()] {
			return utils.BadRequest(c, "missing value for field: "+cf.Label)
		}
	}

	voucher := models.Voucher{
		OrganizationID: orgID,
		Code:           utils.VoucherCode(vt.Name),
		DepartmentID:   *actor.DepartmentID,
		VoucherTypeID:  vtID,
		RaisedByID:     actorID,
		Status:         models.VoucherStatusDraft,
		Tier:           1,
	}

	if err := database.DB.Create(&voucher).Error; err != nil {
		return utils.InternalError(c, err)
	}

	for _, fv := range input.FieldValues {
		cfID, err := uuid.Parse(fv.CustomFieldID)
		if err != nil {
			continue
		}
		database.DB.Create(&models.VoucherFieldValue{
			VoucherID:     voucher.ID,
			CustomFieldID: cfID,
			Value:         fv.Value,
		})
	}

	dupFlag := services.CheckDuplicate(voucher.ID, voucher.VoucherTypeID, orgID)
	database.DB.Create(dupFlag)

	services.WriteAudit(services.AuditInput{
		OrganizationID: &orgID,
		ActorID:        &actorID,
		ActorEmail:     middleware.CurrentEmail(c),
		Action:         models.AuditActionCreate,
		Module:         models.AuditModuleVouchers,
		ResourceID:     voucher.Code,
		Description:    "Created new " + vt.Name + " voucher: " + voucher.Code,
		IPAddress:      middleware.ClientIP(c),
		UserAgent:      c.Get("User-Agent"),
	})

	database.DB.
		Preload("Department", models.SlimDept).
		Preload("VoucherType").
		Preload("RaisedBy", models.SlimUser).
		Preload("FieldValues.CustomField").
		Preload("DuplicateFlag").
		Preload("CurrentApproverDept", models.SlimDept).
		Preload("AmountTier.Steps.Department", models.SlimDept).
		First(&voucher, "id = ?", voucher.ID)

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"data": voucher})
}

func (h *VoucherHandler) Submit(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return utils.BadRequest(c, "invalid id")
	}

	var voucher models.Voucher
	if err := database.DB.First(&voucher, "id = ? AND organization_id = ?", id, orgID).Error; err != nil {
		return utils.NotFound(c, "voucher not found")
	}
	if voucher.Status != models.VoucherStatusDraft {
		return utils.BadRequest(c, "only draft vouchers can be submitted")
	}

	tier, err := services.ResolveTier(voucher.VoucherTypeID, voucher.ID)
	if err != nil {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{"error": err.Error()})
	}

	voucher.Tier = len(tier.Steps)
	voucher.AmountTierID = &tier.ID

	if voucher.Tier == 0 {
		voucher.Status = models.VoucherStatusApproved
		voucher.CurrentApproverDeptID = nil
	} else {
		voucher.Status = models.VoucherStatusPending
		firstDeptID := tier.Steps[0].DepartmentID
		voucher.CurrentApproverDeptID = &firstDeptID
	}

	database.DB.Save(&voucher)

	actorID := middleware.CurrentUserID(c)
	services.WriteAudit(services.AuditInput{
		OrganizationID: &orgID,
		ActorID:        &actorID,
		ActorEmail:     middleware.CurrentEmail(c),
		Action:         models.AuditActionUpdate,
		Module:         models.AuditModuleVouchers,
		ResourceID:     voucher.Code,
		Description:    "Submitted voucher " + voucher.Code + " for approval.",
		IPAddress:      middleware.ClientIP(c),
		UserAgent:      c.Get("User-Agent"),
	})

	database.DB.
		Preload("Department", models.SlimDept).
		Preload("VoucherType").
		Preload("FieldValues.CustomField").
		Preload("CurrentApproverDept", models.SlimDept).
		Preload("AmountTier.Steps.Department", models.SlimDept).
		First(&voucher, "id = ?", id)

	return utils.OK(c, voucher)
}

type approveInput struct {
	Comment string `json:"comment"`
}

func (h *VoucherHandler) Approve(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return utils.BadRequest(c, "invalid id")
	}
	var check models.Voucher
	if err := database.DB.First(&check, "id = ? AND organization_id = ?", id, orgID).Error; err != nil {
		return utils.NotFound(c, "voucher not found")
	}

	var input approveInput
	c.BodyParser(&input)

	actorID := middleware.CurrentUserID(c)
	if err := services.AdvanceApproval(id, actorID, input.Comment); err != nil {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{"error": err.Error()})
	}

	var voucher models.Voucher
	database.DB.
		Preload("Department", models.SlimDept).
		Preload("VoucherType").
		Preload("FieldValues.CustomField").
		Preload("ApprovalHistory.Actor", models.SlimUser).
		Preload("ApprovalHistory.Department", models.SlimDept).
		Preload("CurrentApproverDept", models.SlimDept).
		Preload("AmountTier.Steps.Department", models.SlimDept).
		First(&voucher, "id = ?", id)

	services.WriteAudit(services.AuditInput{
		OrganizationID: &orgID,
		ActorID:        &actorID,
		ActorEmail:     middleware.CurrentEmail(c),
		Action:         models.AuditActionApprove,
		Module:         models.AuditModuleVouchers,
		ResourceID:     voucher.Code,
		Description:    "Approved voucher " + voucher.Code,
		IPAddress:      middleware.ClientIP(c),
		UserAgent:      c.Get("User-Agent"),
	})

	return utils.OK(c, voucher)
}

type rejectInput struct {
	Reason string `json:"reason"`
}

func (h *VoucherHandler) Reject(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return utils.BadRequest(c, "invalid id")
	}
	var check models.Voucher
	if err := database.DB.First(&check, "id = ? AND organization_id = ?", id, orgID).Error; err != nil {
		return utils.NotFound(c, "voucher not found")
	}

	var input rejectInput
	c.BodyParser(&input)

	actorID := middleware.CurrentUserID(c)
	if err := services.RejectVoucher(id, actorID, input.Reason); err != nil {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{"error": err.Error()})
	}

	var voucher models.Voucher
	database.DB.
		Preload("Department", models.SlimDept).
		Preload("VoucherType").
		Preload("FieldValues.CustomField").
		Preload("ApprovalHistory.Actor", models.SlimUser).
		Preload("ApprovalHistory.Department", models.SlimDept).
		Preload("AmountTier.Steps.Department", models.SlimDept).
		First(&voucher, "id = ?", id)

	services.WriteAudit(services.AuditInput{
		OrganizationID: &orgID,
		ActorID:        &actorID,
		ActorEmail:     middleware.CurrentEmail(c),
		Action:         models.AuditActionReject,
		Module:         models.AuditModuleVouchers,
		ResourceID:     voucher.Code,
		Description:    "Rejected voucher " + voucher.Code + ". Reason: " + input.Reason,
		IPAddress:      middleware.ClientIP(c),
		UserAgent:      c.Get("User-Agent"),
	})

	return utils.OK(c, voucher)
}

func (h *VoucherHandler) DismissDuplicate(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return utils.BadRequest(c, "invalid id")
	}
	var check models.Voucher
	if err := database.DB.First(&check, "id = ? AND organization_id = ?", id, orgID).Error; err != nil {
		return utils.NotFound(c, "voucher not found")
	}

	actorID := middleware.CurrentUserID(c)
	result := database.DB.Model(&models.DuplicateFlag{}).
		Where("voucher_id = ?", id).
		Updates(map[string]interface{}{
			"dismissed_by": actorID,
			"dismissed_at": "NOW()",
		})
	if result.RowsAffected == 0 {
		return utils.NotFound(c, "duplicate flag not found")
	}

	return c.JSON(fiber.Map{"message": "duplicate flag dismissed"})
}

func (h *VoucherHandler) Delete(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return utils.BadRequest(c, "invalid id")
	}
	var voucher models.Voucher
	if err := database.DB.First(&voucher, "id = ? AND organization_id = ?", id, orgID).Error; err != nil {
		return utils.NotFound(c, "voucher not found")
	}
	if voucher.Status != models.VoucherStatusDraft {
		return utils.BadRequest(c, "only draft vouchers can be deleted")
	}
	database.DB.Delete(&voucher)
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *VoucherHandler) ListSubmitted(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)
	pg := utils.ParsePagination(c)

	sort := c.Query("sort", "newest")

	query := database.DB.
		Preload("Department", models.SlimDept).
		Preload("VoucherType").
		Preload("RaisedBy", models.SlimUser).
		Preload("DuplicateFlag").
		Preload("ApprovalHistory.Actor", models.SlimUser).
		Preload("ApprovalHistory.Department", models.SlimDept).
		Preload("FieldValues.CustomField").
		Preload("CurrentApproverDept", models.SlimDept).
		Preload("AmountTier.Steps.Department", models.SlimDept).
		Model(&models.Voucher{}).
		Where("vouchers.organization_id = ? AND vouchers.status = ?", orgID, models.VoucherStatusPending)

	switch sort {
	case "oldest":
		query = query.Order("vouchers.created_at ASC")
	default:
		query = query.Order("vouchers.created_at DESC")
	}

	var total int64
	query.Count(&total)

	var vouchers []models.Voucher
	query.Offset(pg.Offset).Limit(pg.Limit).Find(&vouchers)

	return c.JSON(utils.Paginated(vouchers, total, pg))
}

func (h *VoucherHandler) ListMine(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)
	actorID := middleware.CurrentUserID(c)
	pg := utils.ParsePagination(c)

	status := c.Query("status")
	sort := c.Query("sort", "newest")

	query := database.DB.
		Preload("Department", models.SlimDept).
		Preload("VoucherType").
		Preload("RaisedBy", models.SlimUser).
		Preload("DuplicateFlag").
		Preload("ApprovalHistory.Actor", models.SlimUser).
		Preload("ApprovalHistory.Department", models.SlimDept).
		Preload("FieldValues.CustomField").
		Preload("CurrentApproverDept", models.SlimDept).
		Preload("AmountTier.Steps.Department", models.SlimDept).
		Model(&models.Voucher{}).
		Where("vouchers.organization_id = ? AND vouchers.raised_by_id = ?", orgID, actorID)

	if status != "" {
		query = query.Where("vouchers.status = ?", status)
	}

	switch sort {
	case "oldest":
		query = query.Order("vouchers.created_at ASC")
	default:
		query = query.Order("vouchers.created_at DESC")
	}

	var total int64
	query.Count(&total)

	var vouchers []models.Voucher
	query.Offset(pg.Offset).Limit(pg.Limit).Find(&vouchers)

	return c.JSON(utils.Paginated(vouchers, total, pg))
}