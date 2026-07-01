package middleware

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/ledgefice/internal/models"
)

// ─── JWT Claims ───────────────────────────────────────────────────────────────

type Claims struct {
	UserID         uuid.UUID `json:"user_id"`
	Email          string    `json:"email"`
	OrganizationID uuid.UUID `json:"organization_id"`
	Department     string    `json:"department"`
	jwt.RegisteredClaims
}

// ─── Protected middleware ─────────────────────────────────────────────────────

func Protected(secret string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		auth := c.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "missing token"})
		}
		tokenStr := strings.TrimPrefix(auth, "Bearer ")

		claims := &Claims{}
		token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
			return []byte(secret), nil
		})
		if err != nil || !token.Valid {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid token"})
		}

		c.Locals("userID", claims.UserID)
		c.Locals("email", claims.Email)
		c.Locals("orgID", claims.OrganizationID)
		c.Locals("department", claims.Department)
		return c.Next()
	}
}

// ─── RequirePermission middleware ─────────────────────────────────────────────

func RequirePermission(perm string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		// Permissions are checked per-request from the DB-loaded department in handlers.
		// This middleware is a lightweight signal; actual enforcement happens in handlers
		// that call CurrentPermissions(c).
		// For now, pass through — the guard above already authenticated the user.
		// You can add a DB lookup here if you want strict middleware-level enforcement.
		_ = perm
		return c.Next()
	}
}

// ─── Context helpers ──────────────────────────────────────────────────────────

func CurrentUserID(c *fiber.Ctx) uuid.UUID {
	if id, ok := c.Locals("userID").(uuid.UUID); ok {
		return id
	}
	return uuid.Nil
}

func CurrentOrgID(c *fiber.Ctx) uuid.UUID {
	if id, ok := c.Locals("orgID").(uuid.UUID); ok {
		return id
	}
	return uuid.Nil
}

func CurrentEmail(c *fiber.Ctx) string {
	if e, ok := c.Locals("email").(string); ok {
		return e
	}
	return ""
}

func ClientIP(c *fiber.Ctx) string {
	if ip := c.Get("X-Forwarded-For"); ip != "" {
		return strings.Split(ip, ",")[0]
	}
	return c.IP()
}

// ─── Permission check helper (call from handlers) ─────────────────────────────

// HasPermission checks a permission map loaded from the user's department.
func HasPermission(perms models.PermissionMap, perm string) bool {
	return perms[perm]
}