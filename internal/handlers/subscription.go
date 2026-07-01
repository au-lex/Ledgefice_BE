package handlers

import (
	"fmt"
	"os"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/ledgefice/internal/database"
	"github.com/ledgefice/internal/models"
	"github.com/ledgefice/internal/services"
)

type SubscriptionHandler struct {
	Nomba *services.NombaService
}

// Status is a public endpoint — the onboarding owner isn't logged in yet
// when polling this, so no auth guard.
func (h *SubscriptionHandler) Status(c *fiber.Ctx) error {
	ref := c.Params("ref")

	var sub models.Subscription
	if err := database.DB.Where("order_reference = ?", ref).First(&sub).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "subscription not found"})
	}

	return c.JSON(fiber.Map{"status": string(sub.Status)})
}

// Token returns the saved tokenized card details for a subscription, so you can
// confirm it actually got saved after a successful payment. cardPan is already
// masked by Nomba (e.g. 234818********7580) — safe to expose.
func (h *SubscriptionHandler) Token(c *fiber.Ctx) error {
	ref := c.Params("ref")

	var sub models.Subscription
	if err := database.DB.Where("order_reference = ?", ref).First(&sub).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "subscription not found"})
	}

	if sub.TokenKey == "" {
		return c.JSON(fiber.Map{
			"has_token": false,
			"message":   "no tokenized card saved for this subscription yet",
		})
	}

	return c.JSON(fiber.Map{
		"has_token": true,
		"token_key": sub.TokenKey,
		"card_type": sub.CardType,
		"card_pan":  sub.CardPan,
	})
}

// ListTokens returns every subscription that has a saved tokenized card —
// useful for an admin view or just confirming tokens are landing correctly.
func (h *SubscriptionHandler) ListTokens(c *fiber.Ctx) error {
	var subs []models.Subscription
	if err := database.DB.
		Where("token_key IS NOT NULL AND token_key != ''").
		Order("created_at DESC").
		Find(&subs).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to fetch tokens"})
	}

	out := make([]fiber.Map, 0, len(subs))
	for _, s := range subs {
		out = append(out, fiber.Map{
			"subscription_id": s.ID,
			"organization_id": s.OrganizationID,
			"order_reference": s.OrderReference,
			"plan":            s.Plan,
			"token_key":       s.TokenKey,
			"card_type":       s.CardType,
			"card_pan":        s.CardPan,
			"status":          s.Status,
			"paid_at":         s.PaidAt,
		})
	}

	return c.JSON(fiber.Map{"tokens": out})
}

// Renew charges the previously tokenized card for a new billing cycle, using the
// org's existing Subscription record. Triggered manually for now — wire this into
// a cron job once you're ready to automate monthly renewals.
func (h *SubscriptionHandler) Renew(c *fiber.Ctx) error {
	id := c.Params("id") // subscription UUID

	var sub models.Subscription
	if err := database.DB.Where("id = ?", id).First(&sub).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "subscription not found"})
	}

	if sub.TokenKey == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "no tokenized card saved for this subscription"})
	}

	var org models.Organization
	if err := database.DB.Where("id = ?", sub.OrganizationID).First(&org).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "organization not found"})
	}

	var owner models.User
	if err := database.DB.Where("id = ?", *org.OwnerID).First(&owner).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "owner not found"})
	}

	newOrderRef := fmt.Sprintf("renew_%s_%d", sub.OrganizationID.String()[:8], time.Now().Unix())

	result, err := h.Nomba.ChargeTokenizedCard(services.TokenizedChargeInput{
		OrderReference: newOrderRef,
		CustomerEmail:  owner.Email,
		Amount:         sub.Amount,
		Currency:       sub.Currency,
		CallbackURL:    os.Getenv("APP_BASE_URL") + "/payments/nomba/callback",
		TokenKey:       sub.TokenKey,
	})
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "charge failed: " + err.Error()})
	}

	// Create a fresh Subscription row for this billing cycle. The webhook will mark
	// it paid once Nomba confirms — same flow as the original checkout.
	newSub := models.Subscription{
		OrganizationID: sub.OrganizationID,
		Plan:           sub.Plan,
		Amount:         sub.Amount,
		Currency:       sub.Currency,
		OrderReference: newOrderRef,
		Status:         models.SubscriptionStatusPending,
		TokenKey:       sub.TokenKey,
		CardType:       sub.CardType,
		CardPan:        sub.CardPan,
	}
	if err := database.DB.Create(&newSub).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to record renewal"})
	}

	return c.JSON(fiber.Map{
		"order_reference": newOrderRef,
		"charge_status":   result.Status,
		"charge_message":  result.Message,
	})
}