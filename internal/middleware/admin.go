package middleware

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type AdminClaims struct {
	AdminID   uuid.UUID `json:"admin_id"`
	Email     string    `json:"email"`
	TokenType string    `json:"token_type"` 
	jwt.RegisteredClaims
}

// RequireAdmin verifies the token was actually issued by AdminLogin, not a
// regular user login — checks both signature validity AND TokenType == "admin".
func RequireAdmin(secret string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		auth := c.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "missing token"})
		}
		tokenStr := strings.TrimPrefix(auth, "Bearer ")

		claims := &AdminClaims{}
		token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
			return []byte(secret), nil
		})
		if err != nil || !token.Valid {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid token"})
		}
		if claims.TokenType != "admin" {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "not an admin token"})
		}

		c.Locals("adminID", claims.AdminID)
		c.Locals("adminEmail", claims.Email)
		return c.Next()
	}
}

func CurrentAdminID(c *fiber.Ctx) uuid.UUID {
	if id, ok := c.Locals("adminID").(uuid.UUID); ok {
		return id
	}
	return uuid.Nil
}