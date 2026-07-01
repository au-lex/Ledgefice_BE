package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/ledgefice/internal/database"
	"github.com/ledgefice/internal/middleware"
	"github.com/ledgefice/internal/models"
	"github.com/ledgefice/pkg/utils"
)

type AuditHandler struct{}

func (h *AuditHandler) List(c *fiber.Ctx) error {
	pg := utils.ParsePagination(c)

	orgID := middleware.CurrentOrgID(c)
	if orgID == uuid.Nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "missing organization context"})
	}

	module := c.Query("module")
	action := c.Query("action")
	search := c.Query("search")

	query := database.DB.Preload("Actor").
		Model(&models.AuditLog{}).
		Where("organization_id = ?", orgID)

	if module != "" {
		query = query.Where("module = ?", module)
	}
	if action != "" {
		query = query.Where("action = ?", action)
	}
	if search != "" {
		query = query.Where(
			"actor_name ILIKE ? OR description ILIKE ? OR resource_id ILIKE ? OR ip_address ILIKE ?",
			"%"+search+"%", "%"+search+"%", "%"+search+"%", "%"+search+"%",
		)
	}

	query = query.Order("created_at DESC")

	var total int64
	query.Count(&total)

	var logs []models.AuditLog
	query.Offset(pg.Offset).Limit(pg.Limit).Find(&logs)

	return c.JSON(utils.Paginated(logs, total, pg))
}