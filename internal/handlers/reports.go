package handlers

import (
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/ledgefice/internal/database"
	"github.com/ledgefice/internal/models"
	"github.com/ledgefice/pkg/utils"
)

type ReportsHandler struct{}

type reportRange struct {
	Start time.Time
	End   time.Time
}

func parseRange(r string) reportRange {
	now := time.Now()
	switch r {
	case "7d":
		return reportRange{Start: now.AddDate(0, 0, -7), End: now}
	case "90d":
		return reportRange{Start: now.AddDate(0, 0, -90), End: now}
	case "12m":
		return reportRange{Start: now.AddDate(-1, 0, 0), End: now}
	default: // 30d
		return reportRange{Start: now.AddDate(0, 0, -30), End: now}
	}
}

func (h *ReportsHandler) Summary(c *fiber.Ctx) error {
	rng := parseRange(c.Query("range", "30d"))

	var totalVouchers int64
	if err := database.DB.Model(&models.Voucher{}).
		Where("created_at BETWEEN ? AND ?", rng.Start, rng.End).
		Count(&totalVouchers).Error; err != nil {
		return utils.InternalError(c, fmt.Errorf("failed to count vouchers: %w", err))
	}

	var totalSpend float64
	if err := database.DB.Model(&models.Voucher{}).
		Select("COALESCE(SUM(amount), 0)").
		Where("status IN ? AND created_at BETWEEN ? AND ?",
			[]string{"approved", "pending"}, rng.Start, rng.End).
		Scan(&totalSpend).Error; err != nil {
		return utils.InternalError(c, fmt.Errorf("failed to sum total spend: %w", err))
	}

	var approvedValue float64
	if err := database.DB.Model(&models.Voucher{}).
		Select("COALESCE(SUM(amount), 0)").
		Where("status = ? AND created_at BETWEEN ? AND ?",
			models.VoucherStatusApproved, rng.Start, rng.End).
		Scan(&approvedValue).Error; err != nil {
		return utils.InternalError(c, fmt.Errorf("failed to sum approved value: %w", err))
	}

	var rejectedValue float64
	if err := database.DB.Model(&models.Voucher{}).
		Select("COALESCE(SUM(amount), 0)").
		Where("status = ? AND created_at BETWEEN ? AND ?",
			models.VoucherStatusRejected, rng.Start, rng.End).
		Scan(&rejectedValue).Error; err != nil {
		return utils.InternalError(c, fmt.Errorf("failed to sum rejected value: %w", err))
	}

	approvalRate := 0.0
	if totalSpend > 0 {
		approvalRate = (approvedValue / totalSpend) * 100
	}

	return utils.OK(c, fiber.Map{
		"total_vouchers": totalVouchers,
		"total_spend":    totalSpend,
		"approved_value": approvedValue,
		"rejected_value": rejectedValue,
		"approval_rate":  approvalRate,
		"avg_turnaround": "4.2 hrs", // TODO: compute from ApprovalHistory timestamps
	})
}

func (h *ReportsHandler) SpendOverTime(c *fiber.Ctx) error {
	rng := parseRange(c.Query("range", "30d"))

	type row struct {
		Period   string  `json:"label"`
		Value    float64 `json:"value"`
		Approved float64 `json:"approved"`
		Rejected float64 `json:"rejected"`
	}

	// Initialize as empty slice (not nil) so a no-rows result serializes to
	// [] instead of null, and so a query error doesn't get masked by a
	// silently-empty-but-"successful"-looking response.
	rows := []row{}
	if err := database.DB.Raw(`
		SELECT
			TO_CHAR(DATE_TRUNC('day', created_at), 'DD Mon') AS period,
			COALESCE(SUM(amount), 0) AS value,
			COALESCE(SUM(CASE WHEN status = 'approved' THEN amount ELSE 0 END), 0) AS approved,
			COALESCE(SUM(CASE WHEN status = 'rejected' THEN amount ELSE 0 END), 0) AS rejected
		FROM vouchers
		WHERE created_at BETWEEN ? AND ? AND deleted_at IS NULL
		GROUP BY DATE_TRUNC('day', created_at)
		ORDER BY DATE_TRUNC('day', created_at)
	`, rng.Start, rng.End).Scan(&rows).Error; err != nil {
		return utils.InternalError(c, fmt.Errorf("failed to load spend over time: %w", err))
	}

	return utils.OK(c, rows)
}

func (h *ReportsHandler) SpendByDept(c *fiber.Ctx) error {
	type row struct {
		Name  string  `json:"name"`
		Value float64 `json:"value"`
	}
	rows := []row{}
	if err := database.DB.Raw(`
		SELECT d.name, COALESCE(SUM(v.amount), 0) AS value
		FROM departments d
		LEFT JOIN vouchers v ON v.department_id = d.id AND v.status = 'approved' AND v.deleted_at IS NULL
		WHERE d.deleted_at IS NULL
		GROUP BY d.name
		ORDER BY value DESC
	`).Scan(&rows).Error; err != nil {
		return utils.InternalError(c, fmt.Errorf("failed to load spend by department: %w", err))
	}

	return utils.OK(c, rows)
}

func (h *ReportsHandler) VolumeByType(c *fiber.Ctx) error {
	rng := parseRange(c.Query("range", "30d"))

	type row struct {
		Name  string  `json:"name"`
		Count int64   `json:"count"`
		Value float64 `json:"value"`
	}
	rows := []row{}
	if err := database.DB.Raw(`
		SELECT vt.name, COUNT(v.id) AS count, COALESCE(SUM(v.amount), 0) AS value
		FROM voucher_types vt
		LEFT JOIN vouchers v ON v.voucher_type_id = vt.id
			AND v.created_at BETWEEN ? AND ?
			AND v.deleted_at IS NULL
		WHERE vt.deleted_at IS NULL
		GROUP BY vt.name
		ORDER BY value DESC
	`, rng.Start, rng.End).Scan(&rows).Error; err != nil {
		return utils.InternalError(c, fmt.Errorf("failed to load volume by type: %w", err))
	}

	return utils.OK(c, rows)
}