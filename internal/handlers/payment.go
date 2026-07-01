package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/ledgefice/internal/database"
	"github.com/ledgefice/internal/models"
	"github.com/ledgefice/internal/services"
	"gorm.io/gorm"
)

type PaymentHandler struct {
	Nomba *services.NombaService
}

// NombaCallback handles the browser redirect after checkout (set as order.callbackUrl).
// This is for UX only — never trust it to confirm payment. The webhook below is the
// source of truth. This just sends the customer somewhere sensible while the webhook
// catches up in the background.
func (h *PaymentHandler) NombaCallback(c *fiber.Ctx) error {
	ref := c.Query("orderReference")
	frontend := os.Getenv("FRONTEND_BASE_URL")

	if ref == "" {
		return c.Redirect(frontend + "/payment/failed")
	}
	return c.Redirect(frontend + "/payment/pending?ref=" + ref)
}

// nombaWebhookPayload mirrors the payload Nomba sends to the webhook URL
// configured in the dashboard (Settings > Webhooks) — separate from the
// per-order callbackUrl.
type nombaWebhookPayload struct {
	EventType string `json:"event_type"`
	RequestID string `json:"requestId"`
	Data      struct {
		Transaction struct {
			TransactionID     string  `json:"transactionId"`
			Type              string  `json:"type"`
			TransactionAmount float64 `json:"transactionAmount"`
			Time              string  `json:"time"`
		} `json:"transaction"`
		Order struct {
			OrderID        string  `json:"orderId"`
			Amount         float64 `json:"amount"`
			OrderReference string  `json:"orderReference"`
			CustomerEmail  string  `json:"customerEmail"`
			PaymentMethod  string  `json:"paymentMethod"`
			Currency       string  `json:"currency"`
		} `json:"order"`
	} `json:"data"`
}

// NombaWebhook handles the server-to-server webhook configured in the Nomba dashboard.
// Verifies the "nomba-signature" header (HMAC-SHA256 over the raw body using
// NOMBA_WEBHOOK_SECRET) and de-dupes via requestId before applying any state change,
// since webhooks may fire more than once for the same event.
func (h *PaymentHandler) NombaWebhook(c *fiber.Ctx) error {
	rawBody := c.Body()

	signature := c.Get("nomba-signature")
	if signature == "" {
		return c.Status(fiber.StatusUnauthorized).SendString("missing signature")
	}

	secret := os.Getenv("NOMBA_WEBHOOK_SECRET")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(rawBody)
	expected := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(signature), []byte(expected)) {
		return c.Status(fiber.StatusUnauthorized).SendString("bad signature")
	}

	var payload nombaWebhookPayload
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid payload"})
	}

	// Idempotency: only process each requestId once. If the insert fails on the
	// unique constraint, we've already handled this event — ack and stop.
	event := models.WebhookEvent{RequestID: payload.RequestID, EventType: payload.EventType}
	if err := database.DB.Create(&event).Error; err != nil {
		return c.SendStatus(fiber.StatusOK) // duplicate delivery, already processed
	}

	ref := payload.Data.Order.OrderReference
	if ref == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "missing orderReference"})
	}

	var sub models.Subscription
	if err := database.DB.Where("order_reference = ?", ref).First(&sub).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			// Unknown order reference — ack anyway so Nomba doesn't retry forever.
			return c.SendStatus(fiber.StatusOK)
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "lookup failed"})
	}

	switch payload.EventType {
	case "payment_success":
		now := time.Now()
		sub.Status = models.SubscriptionStatusPaid
		sub.PaidAt = &now
		if err := database.DB.Save(&sub).Error; err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update subscription"})
		}

		// Pull the tokenized card so future renewals can charge it directly.
		if cards, err := h.Nomba.GetSavedCards(ref); err == nil && len(cards) > 0 {
			sub.TokenKey = cards[0].TokenKey
			sub.CardType = cards[0].CardType
			sub.CardPan = cards[0].CardPan
			database.DB.Save(&sub)
		}

	case "payment_failed":
		sub.Status = models.SubscriptionStatusFailed
		database.DB.Save(&sub)
	}

	return c.SendStatus(fiber.StatusOK)
}