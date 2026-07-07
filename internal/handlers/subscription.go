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

// MyToken looks up the CURRENT logged-in org's saved card — 
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

// DeleteMyToken removes the org's saved card. 
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
		// Card is gone on Nomba's side at this point — surfacing this so it's
		// not silently inconsistent, but don't treat it as a failed delete.
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "card deleted on Nomba but failed to clear local record"})
	}

	return c.JSON(fiber.Map{"message": "card removed"})
}



// ListTokens returns every subscription that has a saved tokenized card
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
// org's existing Subscription record. 
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




func (h *SubscriptionHandler) MyPlan(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)
	if orgID == uuid.Nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "missing organization context"})
	}

	var org models.Organization
	if err := database.DB.Where("id = ?", orgID).First(&org).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "organization not found"})
	}

	planOrder := []models.PlanType{
		models.PlanStarter,
		models.PlanBusiness,
		models.PlanEnterprise,
	}

	plans := make([]fiber.Map, 0, len(planOrder))
	for _, planType := range planOrder {
		cfg := models.PlanConfigs[planType]
		plans = append(plans, fiber.Map{
			"plan":       planType,
			"config":     cfg,
			"is_current": planType == org.Plan,
		})
	}

	resp := fiber.Map{
		"current_plan": org.Plan,
		"plans":        plans,
	}

	// Latest PAID subscription — this is the one actually driving org.Plan
	// and renewal/dunning behavior. A pending/failed checkout in progress
	// should never override this in the response.
	var paidSub models.Subscription
	if database.DB.
		Where("organization_id = ? AND status = ?", orgID, models.SubscriptionStatusPaid).
		Order("paid_at DESC").
		First(&paidSub).Error == nil {
		resp["subscription"] = fiber.Map{
			"id":              paidSub.ID,
			"plan":            paidSub.Plan,
			"status":          paidSub.Status,
			"order_reference": paidSub.OrderReference,
			"amount":          paidSub.Amount,
			"currency":        paidSub.Currency,
			"paid_at":         paidSub.PaidAt,
			"renews_at":       paidSub.RenewsAt,
			"dunning_stage":   paidSub.DunningStage,
			"cancelled_at":    paidSub.CancelledAt,
		}
	}

	// Separately surface any checkout that's still pending, so the frontend
	// can show "Upgrade to X in progress" without corrupting the main
	// subscription block above.
	var pendingSub models.Subscription
	if database.DB.
		Where("organization_id = ? AND status = ?", orgID, models.SubscriptionStatusPending).
		Order("created_at DESC").
		First(&pendingSub).Error == nil {
		resp["pending_upgrade"] = fiber.Map{
			"id":              pendingSub.ID,
			"plan":            pendingSub.Plan,
			"order_reference": pendingSub.OrderReference,
			"amount":          pendingSub.Amount,
			"currency":        pendingSub.Currency,
			"created_at":      pendingSub.CreatedAt,
		}
	}

	return c.JSON(resp)
}

type UpgradeRequest struct {
	Plan         models.PlanType `json:"plan"`
	BillingCycle string          `json:"billing_cycle"` // "monthly" or "yearly"
}

func (h *SubscriptionHandler) Upgrade(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)
	if orgID == uuid.Nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "missing organization context"})
	}

	var req UpgradeRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	cfg, ok := models.PlanConfigs[req.Plan]
	if !ok {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "unknown plan"})
	}

	var org models.Organization
	if err := database.DB.Where("id = ?", orgID).First(&org).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "organization not found"})
	}

	if org.Plan == req.Plan {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "organization is already on this plan"})
	}

	var owner models.User
	if org.OwnerID == nil {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "organization has no owner on record"})
	}
	if err := database.DB.Where("id = ?", *org.OwnerID).First(&owner).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "owner not found"})
	}

	// TODO: confirm against OnboardingHandler.Setup whether PlanConfig prices
	// (documented as kobo) need /100 conversion here — keep this consistent
	// with however the initial checkout computes its Amount.
	amountKobo := cfg.MonthlyPrice
	if req.BillingCycle == "yearly" {
		amountKobo = cfg.YearlyPrice
	}
	amount := float64(amountKobo) / 100.0

	orderRef := fmt.Sprintf("upgrade_%s_%d", orgID.String()[:8], time.Now().Unix())

	result, err := h.Nomba.CreateCheckoutOrder(services.CheckoutOrderInput{
		OrderReference: orderRef,
		CustomerEmail:  owner.Email,
		Amount:         amount,
		Currency:       "NGN",
		CallbackURL:    os.Getenv("APP_BASE_URL") + "/payments/nomba/callback",
		TokenizeCard:   true,
	})
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create checkout: " + err.Error()})
	}

	// Pending row — the webhook will find this by order_reference, mark it
	// paid, and (see webhook patch below) promote org.Plan once payment clears.
	newSub := models.Subscription{
		OrganizationID: orgID,
		Plan:           req.Plan,
		Amount:         amount,
		Currency:       "NGN",
		OrderReference: orderRef,
		CheckoutLink:   result.CheckoutLink,
		Status:         models.SubscriptionStatusPending,
	}
	if err := database.DB.Create(&newSub).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to record upgrade order"})
	}

	return c.JSON(fiber.Map{
		"checkout_link":   result.CheckoutLink,
		"order_reference": orderRef,
	})
}

// MyHistory returns every subscription row for the logged-in org, most recent
// first — full billing history (upgrades, renewals, failed charges), not just
// the latest one MyPlan returns. Paginated via ?page=&limit= (defaults 1/20).
func (h *SubscriptionHandler) MyHistory(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)
	if orgID == uuid.Nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "missing organization context"})
	}

	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 20)
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit

	var subs []models.Subscription
	var total int64

	database.DB.Model(&models.Subscription{}).
		Where("organization_id = ?", orgID).
		Count(&total)

	if err := database.DB.
		Where("organization_id = ?", orgID).
		Order("created_at DESC").
		Limit(limit).
		Offset(offset).
		Find(&subs).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to fetch subscription history"})
	}

	out := make([]fiber.Map, 0, len(subs))
	for _, s := range subs {
		out = append(out, fiber.Map{
			"id":              s.ID,
			"plan":            s.Plan,
			"amount":          s.Amount,
			"currency":        s.Currency,
			"order_reference": s.OrderReference,
			"status":          s.Status,
			"paid_at":         s.PaidAt,
			"renews_at":       s.RenewsAt,
			"dunning_stage":   s.DunningStage,
			"cancelled_at":    s.CancelledAt,
			"created_at":      s.CreatedAt,
		})
	}

	return c.JSON(fiber.Map{
		"history": out,
		"total":   total,
		"page":    page,
		"limit":   limit,
	})
}