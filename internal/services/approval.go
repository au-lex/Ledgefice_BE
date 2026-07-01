package services

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/ledgefice/internal/database"
	"github.com/ledgefice/internal/models"
)

func extractAmount(voucherID uuid.UUID) float64 {
	var fv models.VoucherFieldValue
	err := database.DB.
		Joins("JOIN custom_fields ON custom_fields.id = voucher_field_values.custom_field_id").
		Where("voucher_field_values.voucher_id = ? AND custom_fields.field_type = ?", voucherID, models.FieldTypeNumber).
		First(&fv).Error
	if err != nil {
		return 0
	}
	val, _ := strconv.ParseFloat(fv.Value, 64)
	return val
}

func ResolveTier(voucherTypeID uuid.UUID, voucherID uuid.UUID) (*models.AmountTier, error) {
	var chain models.ApprovalChain
	if err := database.DB.
		Preload("Tiers.Steps").
		Where("voucher_type_id = ?", voucherTypeID).
		First(&chain).Error; err != nil {
		return nil, fmt.Errorf("no approval chain configured for this voucher type")
	}

	amount := extractAmount(voucherID)

	for _, tier := range chain.Tiers {
		if amount >= tier.MinAmount {
			if tier.MaxAmount == nil || amount <= *tier.MaxAmount {
				return &tier, nil
			}
		}
	}
	return nil, errors.New("no amount tier found for the specified amount")
}

func AdvanceApproval(voucherID uuid.UUID, actorID uuid.UUID, comment string) error {
	var voucher models.Voucher
	if err := database.DB.First(&voucher, "id = ?", voucherID).Error; err != nil {
		return err
	}
	if voucher.Status != models.VoucherStatusPending {
		return errors.New("voucher is not pending approval")
	}
	if voucher.CurrentApproverDeptID == nil {
		return errors.New("no approver department set on voucher")
	}

	var actor models.User
	if err := database.DB.First(&actor, "id = ?", actorID).Error; err != nil {
		return errors.New("actor not found")
	}
	if actor.DepartmentID == nil || *actor.DepartmentID != *voucher.CurrentApproverDeptID {
		return errors.New("you are not in the department authorized to approve this voucher")
	}

	now := time.Now()
	database.DB.Create(&models.ApprovalHistory{
		VoucherID:    voucherID,
		ActorID:      &actorID,
		DepartmentID: *voucher.CurrentApproverDeptID,
		Action:       models.ApprovalActionApproved,
		Comment:      comment,
		ActedAt:      &now,
	})

	tier, err := ResolveTier(voucher.VoucherTypeID, voucherID)
	if err != nil {
		return err
	}

	currentIndex := -1
	for i, step := range tier.Steps {
		if step.DepartmentID == *voucher.CurrentApproverDeptID {
			currentIndex = i
			break
		}
	}
	if currentIndex < 0 {
		return fmt.Errorf("current approver department is not a valid step in this tier")
	}

	if currentIndex+1 < len(tier.Steps) {
		nextDeptID := tier.Steps[currentIndex+1].DepartmentID
		voucher.CurrentApproverDeptID = &nextDeptID
	} else {
		voucher.Status = models.VoucherStatusApproved
		voucher.CurrentApproverDeptID = nil
	}

	return database.DB.Save(&voucher).Error
}

func RejectVoucher(voucherID uuid.UUID, actorID uuid.UUID, reason string) error {
	var voucher models.Voucher
	if err := database.DB.First(&voucher, "id = ?", voucherID).Error; err != nil {
		return err
	}
	if voucher.Status != models.VoucherStatusPending {
		return errors.New("voucher is not pending approval")
	}
	if voucher.CurrentApproverDeptID == nil {
		return errors.New("no approver department set on voucher")
	}

	var actor models.User
	if err := database.DB.First(&actor, "id = ?", actorID).Error; err != nil {
		return errors.New("actor not found")
	}
	if actor.DepartmentID == nil || *actor.DepartmentID != *voucher.CurrentApproverDeptID {
		return errors.New("you are not in the department authorized to reject this voucher")
	}

	now := time.Now()
	database.DB.Create(&models.ApprovalHistory{
		VoucherID:    voucherID,
		ActorID:      &actorID,
		DepartmentID: *voucher.CurrentApproverDeptID,
		Action:       models.ApprovalActionRejected,
		Comment:      reason,
		ActedAt:      &now,
	})

	voucher.Status = models.VoucherStatusRejected
	voucher.CurrentApproverDeptID = nil
	return database.DB.Save(&voucher).Error
}