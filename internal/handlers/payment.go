package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
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

// NombaCallback handles browser redirect
func (h *PaymentHandler) NombaCallback(c *fiber.Ctx) error {
	ref := c.Query("orderReference")
	frontend := os.Getenv("FRONTEND_BASE_URL")

	fmt.Println("🔥 CALLBACK HIT")
	fmt.Println("Order Reference:", ref)

	if ref == "" {
		fmt.Println("❌ Missing orderReference")
		return c.Redirect(frontend + "/payment/failed")
	}

	return c.Redirect(frontend + "/payment/pending?ref=" + ref)
}

type nombaWebhookPayload struct {
	EventType string `json:"event_type"`
	RequestID string `json:"requestId"`

	Data struct {
		Merchant struct {
			UserID   string `json:"userId"`
			WalletID string `json:"walletId"`
		} `json:"merchant"`

		// TokenizedCardData is included directly on payment_success webhooks
		// for card_payment orders — reading it here avoids a second,
		// racy call to GetSavedCards right after the webhook fires (Nomba's
		// vault write and the webhook dispatch aren't guaranteed to be
		// synchronous, so querying immediately after can return zero cards).
		TokenizedCardData struct {
			TokenKey         string `json:"tokenKey"`
			CardType         string `json:"cardType"`
			TokenExpiryYear  string `json:"tokenExpiryYear"`
			TokenExpiryMonth string `json:"tokenExpiryMonth"`
			CardPan          string `json:"cardPan"`
		} `json:"tokenizedCardData"`

		Transaction struct {
			TransactionID     string  `json:"transactionId"`
			Type              string  `json:"type"`
			TransactionAmount float64 `json:"transactionAmount"`
			Time              string  `json:"time"`
			ResponseCode      string  `json:"responseCode"`
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


func fetchNewestCardWithRetry(nomba *services.NombaService, email string) (*services.TokenizedCard, error) {
	var lastErr error
	delays := []time.Duration{0, 2 * time.Second, 5 * time.Second, 10 * time.Second}

	for _, d := range delays {
		if d > 0 {
			time.Sleep(d)
		}

		cards, err := nomba.GetSavedCards(email)
		if err != nil {
			lastErr = err
			continue
		}
		if len(cards) == 0 {
			lastErr = fmt.Errorf("no cards found yet for %s", email)
			continue
		}

		newest := cards[0]
		for _, c := range cards[1:] {
			if c.TokenExpirationDate > newest.TokenExpirationDate {
				newest = c
			}
		}
		return &newest, nil
	}

	return nil, lastErr
}

// NombaWebhook handles server-to-server webhook from Nomba
func (h *PaymentHandler) NombaWebhook(c *fiber.Ctx) error {

	fmt.Println("\n🔥🔥🔥 NOMBA WEBHOOK RECEIVED 🔥🔥🔥")

	rawBody := c.Body()

	fmt.Println("BODY:")
	fmt.Println(string(rawBody))

	signature := c.Get("nomba-signature")
	timestamp := c.Get("nomba-timestamp")

	fmt.Println("HEADERS:")
	fmt.Println("signature:", signature)
	fmt.Println("algorithm:", c.Get("nomba-signature-algorithm"))
	fmt.Println("version:", c.Get("nomba-signature-version"))
	fmt.Println("timestamp:", timestamp)

	if signature == "" {
		fmt.Println("❌ Missing signature")
		return c.Status(401).SendString("missing signature")
	}

	if timestamp == "" {
		fmt.Println("❌ Missing timestamp")
		return c.Status(401).SendString("missing timestamp")
	}

	var payload nombaWebhookPayload

	if err := json.Unmarshal(rawBody, &payload); err != nil {
		fmt.Println("❌ JSON ERROR:", err)
		return c.Status(400).JSON(fiber.Map{"error": "invalid payload"})
	}

	responseCode := payload.Data.Transaction.ResponseCode
	if responseCode == "null" {
		responseCode = ""
	}

	signedString := fmt.Sprintf(
		"%s:%s:%s:%s:%s:%s:%s:%s:%s",
		payload.EventType,
		payload.RequestID,
		payload.Data.Merchant.UserID,
		payload.Data.Merchant.WalletID,
		payload.Data.Transaction.TransactionID,
		payload.Data.Transaction.Type,
		payload.Data.Transaction.Time,
		responseCode,
		timestamp,
	)

	secret := os.Getenv("NOMBA_WEBHOOK_SECRET")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signedString))
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(signature), []byte(expected)) {
		fmt.Println("❌ BAD SIGNATURE")
		return c.Status(401).SendString("bad signature")
	}

	fmt.Println("✅ SIGNATURE VERIFIED")
	fmt.Println("EVENT:", payload.EventType)
	fmt.Println("REQUEST ID:", payload.RequestID)
	fmt.Println("ORDER REF:", payload.Data.Order.OrderReference)

	ref := payload.Data.Order.OrderReference

	if ref == "" {
		fmt.Println("❌ Missing order reference")
		return c.Status(400).JSON(fiber.Map{"error": "missing orderReference"})
	}

	// ─── Look up an existing Subscription first (renewal/upgrade path — org
	// already exists in this case, since Renew() and Upgrade() only run for
	// existing orgs) ──────────────────────────────────────────────────────────
	var sub models.Subscription
	err := database.DB.Where("order_reference = ?", ref).First(&sub).Error

	if err != nil && err != gorm.ErrRecordNotFound {
		fmt.Println("DB ERROR:", err)
		return c.SendStatus(500)
	}

	subFound := err == nil

	// ─── No existing Subscription — check for a PendingSignup instead. This is
	// the first-time-signup path: the account doesn't exist yet, and only gets
	// created here, now that payment_success has actually been confirmed. ────
	if !subFound {
		if payload.EventType != "payment_success" {
			fmt.Println("ℹ️ No subscription found and event is not payment_success — nothing to do")
			return c.SendStatus(200)
		}

		var pending models.PendingSignup
		if err := database.DB.Where("order_reference = ?", ref).First(&pending).Error; err != nil {
			fmt.Println("❌ No subscription or pending signup found for order_reference:", ref)
			return c.SendStatus(200)
		}

		fmt.Println("🆕 PENDING SIGNUP FOUND — creating account now that payment is confirmed:", pending.OrganizationName)

		createdSub, err := createAccountFromPendingSignup(pending)
		if err != nil {
			// The customer HAS paid at this point — this failure needs manual
			// follow-up (e.g. org name collided with one created in the
			// meantime). Don't silently swallow this; it needs a real alert
			// in production, not just a log line.
			fmt.Println("❌ FAILED TO CREATE ACCOUNT FROM PENDING SIGNUP:", err)
			return c.SendStatus(500)
		}

		sub = *createdSub
		fmt.Println("✅ ACCOUNT CREATED — organization_id:", sub.OrganizationID)
	}

	fmt.Println("SUB FOUND/CREATED:", sub.ID)

	// ─── Dedup guard — scoped so it only skips work that's already genuinely
	// complete. A duplicate delivery for a sub that already has its token
	// saved is truly a no-op; a duplicate delivery for a sub that's paid but
	// still missing TokenKey (e.g. a prior fallback attempt failed) is
	// allowed through, since Nomba's own retry delivery is exactly the
	// second chance needed to fix that.
	event := models.WebhookEvent{
		RequestID: payload.RequestID,
		EventType: payload.EventType,
	}
	isDuplicateDelivery := database.DB.Create(&event).Error != nil

	if isDuplicateDelivery {
		alreadyComplete := sub.Status == models.SubscriptionStatusPaid &&
			(sub.TokenKey != "" || payload.Data.Order.PaymentMethod == "bank_transfer")
		if alreadyComplete {
			fmt.Println("⚠️ Duplicate webhook, and token/status already settled — skipping")
			return c.SendStatus(200)
		}
		fmt.Println("⚠️ Duplicate webhook delivery, but token save previously incomplete — retrying")
	}

	switch payload.EventType {

	case "payment_success":
		fmt.Println("💰 PAYMENT SUCCESS")

		now := time.Now()
		sub.Status = models.SubscriptionStatusPaid
		sub.PaidAt = &now

		nextRenewal := now.AddDate(0, 1, 0)
		sub.RenewsAt = &nextRenewal
		sub.RetryCount = 0
		sub.NextRetryAt = nil
		sub.DunningStage = ""
		sub.LastRetryError = ""

		if err := database.DB.Save(&sub).Error; err != nil {
			fmt.Println("❌ UPDATE FAILED:", err)
			return c.SendStatus(500)
		}

		fmt.Println("✅ SUBSCRIPTION UPDATED TO PAID")

		// ─── Promote the org to this subscription's plan. No-op for renewals
		// (Renew() always carries over the same plan), but this is the step
		// that actually completes an upgrade/downgrade initiated via
		// SubscriptionHandler.Upgrade — org.Plan is what everything else
		// (limits, feature gates) actually reads. ──────────────────────────
		var org models.Organization
		if err := database.DB.Where("id = ?", sub.OrganizationID).First(&org).Error; err != nil {
			fmt.Println("⚠️ FAILED TO LOAD ORG FOR PLAN SYNC:", err)
		} else if org.Plan != sub.Plan {
			oldPlan := org.Plan
			org.Plan = sub.Plan
			if err := database.DB.Save(&org).Error; err != nil {
				// Payment succeeded and the subscription row is correct, but
				// the org's plan didn't update — needs the same kind of
				// manual-follow-up alert as the pending-signup failure above,
				// since the customer is now paying for a plan they don't have.
				fmt.Println("❌ FAILED TO UPDATE ORG PLAN AFTER PAYMENT:", err)
			} else {
				fmt.Println("✅ ORG PLAN CHANGED:", oldPlan, "→", org.Plan)
			}
		}

		// ─── Save the tokenized card ────────────────────────────────────────
		if payload.Data.Order.PaymentMethod == "bank_transfer" {
			fmt.Println("ℹ️ bank_transfer payment — no card to tokenize, skipping card save")
		} else if payload.Data.TokenizedCardData.TokenKey != "" {
			// Primary path: the token is already in the webhook payload, so
			// there's no extra API call and no race condition to worry about.
			sub.TokenKey = payload.Data.TokenizedCardData.TokenKey
			sub.CardType = payload.Data.TokenizedCardData.CardType
			sub.CardPan = payload.Data.TokenizedCardData.CardPan
			if err := database.DB.Save(&sub).Error; err != nil {
				fmt.Println("❌ FAILED TO SAVE CARD TOKEN FROM PAYLOAD:", err)
			} else {
				fmt.Println("✅ CARD TOKEN SAVED (from webhook payload)")
			}
		} else {
			// Fallback: only reached if Nomba ever sends a payment_success
			// webhook without tokenizedCardData populated. Keeps the retry
			// logic as a safety net rather than the primary mechanism.
			fmt.Println("⚠️ tokenizedCardData missing from webhook payload — falling back to API lookup")
			card, err := fetchNewestCardWithRetry(h.Nomba, payload.Data.Order.CustomerEmail)
			if err != nil {
				fmt.Println("❌ TOKEN FETCH FAILED AFTER RETRIES:", err)
				sub.LastRetryError = "token fetch failed: " + err.Error()
				database.DB.Save(&sub)
			} else if card != nil {
				sub.TokenKey = card.TokenKey
				sub.CardType = card.CardType
				sub.CardPan = card.CardPan
				database.DB.Save(&sub)
				fmt.Println("✅ CARD TOKEN SAVED (fallback API lookup)")
			}
		}

	case "payment_failed":
		fmt.Println("❌ PAYMENT FAILED")
		sub.Status = models.SubscriptionStatusFailed
		database.DB.Save(&sub)
	}

	fmt.Println("🔥 WEBHOOK COMPLETED")

	return c.SendStatus(200)
}

func createAccountFromPendingSignup(pending models.PendingSignup) (*models.Subscription, error) {
	tx := database.DB.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	org := models.Organization{
		Name:            pending.OrganizationName,
		LogoURL:         pending.LogoURL,
		NumberOfWorkers: pending.NumberOfWorkers,
		Plan:            pending.Plan,
	}
	if err := tx.Create(&org).Error; err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("organization name already exists or failed to create: %w", err)
	}

	owner := models.User{
		OrganizationID: org.ID,
		Name:           pending.OrganizationName + " Admin",
		Email:          pending.Email,
		Password:       pending.PasswordHash,
		Status:         models.UserStatusActive,
	}
	if err := tx.Create(&owner).Error; err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("failed to create owner user: %w", err)
	}

	org.OwnerID = &owner.ID
	if err := tx.Save(&org).Error; err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("failed to link owner to org: %w", err)
	}

	ownerDept := models.Department{
		OrganizationID: org.ID,
		Name:           "Owner",
		Permissions:    models.FullPermissions(),
	}
	if err := tx.Create(&ownerDept).Error; err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("failed to create owner department: %w", err)
	}

	owner.DepartmentID = &ownerDept.ID
	if err := tx.Save(&owner).Error; err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("failed to assign owner department: %w", err)
	}

	sub := models.Subscription{
		OrganizationID: org.ID,
		Plan:           pending.Plan,
		Amount:         pending.Amount,
		Currency:       pending.Currency,
		OrderReference: pending.OrderReference,
		Status:         models.SubscriptionStatusPending,
	}
	if err := tx.Create(&sub).Error; err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("failed to create subscription record: %w", err)
	}

	if err := tx.Delete(&pending).Error; err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("failed to clear pending signup: %w", err)
	}

	if err := tx.Commit().Error; err != nil {
		return nil, fmt.Errorf("failed to commit account creation: %w", err)
	}

	return &sub, nil
}