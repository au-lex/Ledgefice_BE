package handlers

import (
	"fmt"
	"os"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/ledgefice/internal/database"
	"github.com/ledgefice/internal/middleware"
	"github.com/ledgefice/internal/models"
	"github.com/ledgefice/internal/services"
)

type SubscriptionHandler struct {
	Nomba *services.NombaService
}

// Status is a public endpoint — the onboarding owner isn't logged in yet
// when polling this, so no auth guard.
// Status is polled by the frontend after checkout redirect. Account creation
// is now deferred until payment_success (see PaymentHandler.NombaWebhook), so
// there are three possible states here, not two:
//   - No Subscription AND no PendingSignup found → genuinely invalid ref (404)
//   - No Subscription but a PendingSignup exists → still waiting on payment;
//     the account doesn't exist yet, report "awaiting_payment"
//   - Subscription exists → normal status (pending/paid/failed), and once
//     paid, organization_id/owner_id are included so the frontend can move
//     on to login instead of just showing a generic "success" screen.
func (h *SubscriptionHandler) Status(c *fiber.Ctx) error {
	ref := c.Params("ref")

	var sub models.Subscription
	if err := database.DB.Where("order_reference = ?", ref).First(&sub).Error; err == nil {
		resp := fiber.Map{"status": string(sub.Status)}
		if sub.Status == models.SubscriptionStatusPaid {
			resp["organization_id"] = sub.OrganizationID

			var org models.Organization
			if err := database.DB.Where("id = ?", sub.OrganizationID).First(&org).Error; err == nil && org.OwnerID != nil {
				resp["owner_id"] = *org.OwnerID
			}
		}
		return c.JSON(resp)
	}

	var pending models.PendingSignup
	if err := database.DB.Where("order_reference = ?", ref).First(&pending).Error; err == nil {
		return c.JSON(fiber.Map{"status": "awaiting_payment"})
	}

	return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "subscription not found"})
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

// MyToken looks up the CURRENT logged-in org's saved card — the version you
// want for an individual account, since the frontend won't have an old
// order_reference lying around once the user is authenticated. Assumes
// middleware.Protected sets "organization_id" in c.Locals — adjust the key
// below if your JWT claims use a different name.
func (h *SubscriptionHandler) MyToken(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)
	if orgID == uuid.Nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "missing organization context"})
	}

	var sub models.Subscription
	if err := database.DB.
		Where("organization_id = ? AND token_key IS NOT NULL AND token_key != ''", orgID).
		Order("created_at DESC").
		First(&sub).Error; err != nil {
		return c.JSON(fiber.Map{
			"has_token": false,
			"message":   "no tokenized card saved for this organization yet",
		})
	}

	return c.JSON(fiber.Map{
		"has_token":       true,
		"subscription_id": sub.ID,
		"token_key":       sub.TokenKey,
		"card_type":       sub.CardType,
		"card_pan":        sub.CardPan,
	})
}

// DeleteMyToken removes the org's saved card — both from Nomba's vault and
// from the local subscription record, so renewals stop trying to charge it.
func (h *SubscriptionHandler) DeleteMyToken(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)
	if orgID == uuid.Nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "missing organization context"})
	}

	var sub models.Subscription
	if err := database.DB.
		Where("organization_id = ? AND token_key IS NOT NULL AND token_key != ''", orgID).
		Order("created_at DESC").
		First(&sub).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "no saved card found"})
	}

	if err := h.Nomba.DeleteTokenizedCard(sub.TokenKey); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to delete card: " + err.Error()})
	}

	sub.TokenKey = ""
	sub.CardType = ""
	sub.CardPan = ""
	if err := database.DB.Save(&sub).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "card deleted on Nomba but failed to clear local record"})
	}

	return c.JSON(fiber.Map{"message": "card removed"})
}

// MyLiveToken queries Nomba directly (not the local DB) for the logged-in org
// owner's tokenized card — use this right before showing a "cancel subscription /
// remove card" screen, so the user sees the real current state even if the local
// DB drifted out of sync with Nomba's vault.
func (h *SubscriptionHandler) MyLiveToken(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)
	if orgID == uuid.Nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "missing organization context"})
	}

	var org models.Organization
	if err := database.DB.Where("id = ?", orgID).First(&org).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "organization not found"})
	}

	var owner models.User
	if err := database.DB.Where("id = ?", *org.OwnerID).First(&owner).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "owner not found"})
	}

	cards, err := h.Nomba.GetSavedCards(owner.Email)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to fetch card from Nomba: " + err.Error()})
	}

	if len(cards) == 0 {
		return c.JSON(fiber.Map{
			"has_token": false,
			"message":   "no tokenized card found on Nomba for this account",
		})
	}

	out := make([]fiber.Map, 0, len(cards))
	for _, card := range cards {
		out = append(out, fiber.Map{
			"token_key": card.TokenKey,
			"card_type": card.CardType,
			"card_pan":  card.CardPan,
		})
	}

	return c.JSON(fiber.Map{
		"has_token": true,
		"cards":     out,
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
// Renew handles both renewal paths: tokenized card (instant, webhook confirms
// async) and Direct Debit mandate (DebitMandate call itself returns the final
// status synchronously — no webhook involved for this path, per Nomba's docs).
func (h *SubscriptionHandler) Renew(c *fiber.Ctx) error {
	id := c.Params("id") // subscription UUID

	var sub models.Subscription
	if err := database.DB.Where("id = ?", id).First(&sub).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "subscription not found"})
	}

	if sub.TokenKey == "" && sub.MandateID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "no tokenized card or active mandate saved for this subscription"})
	}

	var org models.Organization
	if err := database.DB.Where("id = ?", sub.OrganizationID).First(&org).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "organization not found"})
	}

	var owner models.User
	if err := database.DB.Where("id = ?", *org.OwnerID).First(&owner).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "owner not found"})
	}

	// ─── Mandate path ────────────────────────────────────────────────────────
	if sub.TokenKey == "" && sub.MandateID != "" {
		result, err := h.Nomba.DebitMandate(sub.MandateID, sub.Amount)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "mandate debit failed: " + err.Error()})
		}

		newOrderRef := fmt.Sprintf("renew_%s_%d", sub.OrganizationID.String()[:8], time.Now().Unix())
		status := models.SubscriptionStatusPending
		var paidAt *time.Time
		if result.Status == "SUCCESS" {
			status = models.SubscriptionStatusPaid
			now := time.Now()
			paidAt = &now
		} else {
			status = models.SubscriptionStatusFailed
		}

		newSub := models.Subscription{
			OrganizationID:  sub.OrganizationID,
			Plan:            sub.Plan,
			Amount:          sub.Amount,
			Currency:        sub.Currency,
			OrderReference:  newOrderRef,
			Status:          status,
			PaidAt:          paidAt,
			MandateID:       sub.MandateID,
			MandateStatus:   sub.MandateStatus,
			MandateBankCode: sub.MandateBankCode,
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

	// ─── Tokenized card path ─────────────────────────────────────────────────
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