package utils

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	// "github.com/ledgefice/internal/middleware"
)

// ─── JWT ─────────────────────────────────────────────────────────────────────
type TokenClaims struct {
	UserID         uuid.UUID `json:"user_id"`
	Email          string    `json:"email"`
	OrganizationID uuid.UUID `json:"organization_id"`
	Department     string    `json:"department"`
	jwt.RegisteredClaims
}

// GenerateToken creates a signed JWT embedding the user's org so every
// downstream request can scope queries without an extra DB lookup.
func GenerateToken(
	userID uuid.UUID,
	email string,
	orgID uuid.UUID,
	department string,
	secret string,
	expiresIn string,
) (string, error) {
	duration, err := time.ParseDuration(expiresIn)
	if err != nil {
		duration = 24 * time.Hour
	}

	claims := TokenClaims{
		UserID:         userID,
		Email:          email,
		OrganizationID: orgID,
		Department:     department,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(duration)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// ─── Pagination ───────────────────────────────────────────────────────────────

type Pagination struct {
	Page    int `json:"page"`
	Limit   int `json:"limit"`
	Offset  int `json:"-"`
}

func ParsePagination(c *fiber.Ctx) Pagination {
	page, _ := strconv.Atoi(c.Query("page", "1"))
	limit, _ := strconv.Atoi(c.Query("limit", "20"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	return Pagination{
		Page:   page,
		Limit:  limit,
		Offset: (page - 1) * limit,
	}
}

type PagedResponse struct {
	Data       interface{} `json:"data"`
	Page       int         `json:"page"`
	Limit      int         `json:"limit"`
	TotalItems int64       `json:"total_items"`
	TotalPages int64       `json:"total_pages"`
}

func Paginated(data interface{}, total int64, pg Pagination) PagedResponse {
	pages := total / int64(pg.Limit)
	if total%int64(pg.Limit) > 0 {
		pages++
	}
	return PagedResponse{
		Data:       data,
		Page:       pg.Page,
		Limit:      pg.Limit,
		TotalItems: total,
		TotalPages: pages,
	}
}

// ─── Voucher Code Generator ───────────────────────────────────────────────────
// Pattern:  <TYPE_PREFIX>-<YEAR>-<SEQUENCE>
// e.g.      CPY-2024-0041

var typePrefixes = map[string]string{
	"Contractor Payment": "CPY",
	"Petty Cash":         "PCH",
	"Site Materials":     "SMR",
	"Equipment Hire":     "EQH",
}

func VoucherCode(typeName string) string {
	prefix, ok := typePrefixes[typeName]
	if !ok {
		prefix = strings.ToUpper(typeName[:3])
	}
	year := time.Now().Year()
	seq := rand.Intn(9000) + 1000 // placeholder — replace with DB sequence
	return fmt.Sprintf("%s-%d-%04d", prefix, year, seq)
}

// ─── Error Helpers ────────────────────────────────────────────────────────────

func BadRequest(c *fiber.Ctx, msg string) error {
	return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": msg})
}

func NotFound(c *fiber.Ctx, msg string) error {
	return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": msg})
}

func InternalError(c *fiber.Ctx, err error) error {
	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
}

func OK(c *fiber.Ctx, data interface{}) error {
	return c.JSON(fiber.Map{"data": data})
}


