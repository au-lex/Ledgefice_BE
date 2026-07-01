// services/duplicate.go
package services

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/ledgefice/internal/database"
	"github.com/ledgefice/internal/models"
)

// CheckDuplicate detects duplicates by matching org + voucher type + number field value
// within 30 days. No more vendor/invoice — those are custom fields now.
func CheckDuplicate(voucherID uuid.UUID, voucherTypeID uuid.UUID, orgID uuid.UUID) *models.DuplicateFlag {
	amount := extractAmount(voucherID)
	if amount == 0 {
		return &models.DuplicateFlag{VoucherID: voucherID, IsDuplicate: false}
	}

	window := time.Now().AddDate(0, 0, -30)

	// Find vouchers of same type in same org with a number field of same value in last 30 days
	var matchFV models.VoucherFieldValue
	err := database.DB.
		Joins("JOIN custom_fields ON custom_fields.id = voucher_field_values.custom_field_id").
		Joins("JOIN vouchers ON vouchers.id = voucher_field_values.voucher_id").
		Where(`voucher_field_values.voucher_id != ?`, voucherID).
		Where(`vouchers.organization_id = ?`, orgID).
		Where(`vouchers.voucher_type_id = ?`, voucherTypeID).
		Where(`vouchers.status != ?`, models.VoucherStatusRejected).
		Where(`custom_fields.field_type = ?`, models.FieldTypeNumber).
		Where(`voucher_field_values.value = ?`, fmt.Sprintf("%g", amount)).
		Where(`vouchers.created_at >= ?`, window).
		First(&matchFV).Error

	if err == nil {
		var matchVoucher models.Voucher
		database.DB.First(&matchVoucher, "id = ?", matchFV.VoucherID)
		return &models.DuplicateFlag{
			VoucherID:   voucherID,
			IsDuplicate: true,
			Reason:      "Matching amount found in a voucher of the same type within the last 30 days.",
			MatchRef:    matchVoucher.Code,
		}
	}

	return &models.DuplicateFlag{VoucherID: voucherID, IsDuplicate: false}
}