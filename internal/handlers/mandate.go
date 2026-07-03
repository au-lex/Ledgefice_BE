package handlers

import (
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/ledgefice/internal/database"
	"github.com/ledgefice/internal/middleware"
	"github.com/ledgefice/internal/models"
	"github.com/ledgefice/internal/services"
)

type MandateHandler struct {
	Nomba *services.NombaService
}

// ListBanks returns Nomba's supported bank list for the bank-picker dropdown.

func (h *MandateHandler) ListBanks(c *fiber.Ctx) error {
	banks, err := h.Nomba.ListBanks()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to fetch banks: " + err.Error()})
	}
	return c.JSON(fiber.Map{"banks": banks})
}

type lookupRequest struct {
	AccountNumber string `json:"account_number"`
	BankCode      string `json:"bank_code"`
}

// LookupAccount resolves an account number + bank code to the account holder's
// name, so the frontend can show "Is this you?" before creating the mandate.
func (h *MandateHandler) LookupAccount(c *fiber.Ctx) error {
	var req lookupRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.AccountNumber == "" || req.BankCode == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "account_number and bank_code are required"})
	}

	result, err := h.Nomba.LookupBankAccount(req.AccountNumber, req.BankCode)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "account lookup failed: " + err.Error()})
	}

	return c.JSON(fiber.Map{
		"account_number": result.AccountNumber,
		"account_name":   result.AccountName,
	})
}

// GetMandateStatus checks whether a mandate has moved from pending (created,
// awaiting the customer's NIBSS token-payment authentication) to active.
func (h *MandateHandler) GetMandateStatus(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)
	if orgID == uuid.Nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "missing organization context"})
	}

	var sub models.Subscription
	if err := database.DB.
		Where("organization_id = ? AND mandate_id != ''", orgID).
		Order("created_at DESC").
		First(&sub).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "no mandate found for this organization"})
	}

	result, err := h.Nomba.GetMandateStatus(sub.MandateID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to fetch mandate status: " + err.Error()})
	}

	// Nomba returns capitalized strings ("Active"), map to this codebase's
	
	switch result.MandateStatus {
	case "Active":
		sub.MandateStatus = models.MandateStatusActive
	case "Failed", "Rejected":
		sub.MandateStatus = models.MandateStatusFailed
	default:
		sub.MandateStatus = models.MandateStatusPending
	}
	database.DB.Save(&sub)

	return c.JSON(fiber.Map{
		"mandate_id":     result.MandateID,
		"mandate_status": result.MandateStatus,
		"rejection_comment": result.RejectionComment,
	})
}

type createMandateRequest struct {
	AccountNumber string `json:"account_number"`
	BankCode      string `json:"bank_code"`
	AccountName   string `json:"account_name"` // the resolved name from LookupAccount, confirmed by the user
}

// CreateMandate sets up recurring direct-debit billing for the logged-in org's
// most recent subscription — the fallback path for orgs that paid via
// bank_transfer and have no tokenized card to renew against. The mandate
// itself doesn't charge the subscription amount; it triggers a small NIBSS
// token-payment step the customer must complete to authenticate it. 
func (h *MandateHandler) CreateMandate(c *fiber.Ctx) error {
	orgID := middleware.CurrentOrgID(c)
	if orgID == uuid.Nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "missing organization context"})
	}

	var req createMandateRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.AccountNumber == "" || req.BankCode == "" || req.AccountName == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "account_number, bank_code, and account_name are required"})
	}

	var org models.Organization
	if err := database.DB.Where("id = ?", orgID).First(&org).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "organization not found"})
	}

	var owner models.User
	if err := database.DB.Where("id = ?", *org.OwnerID).First(&owner).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "owner not found"})
	}

	var sub models.Subscription
	if err := database.DB.
		Where("organization_id = ?", orgID).
		Order("created_at DESC").
		First(&sub).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "no subscription found for this organization"})
	}

	if sub.TokenKey != "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "this subscription already has a tokenized card — mandate not needed"})
	}
	if sub.MandateStatus == models.MandateStatusActive {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "an active mandate already exists for this subscription"})
	}

	now := time.Now()
	merchantRef := fmt.Sprintf("%d", now.UnixNano()) // must be NUMERIC per Nomba's spec
	start := now.Add(5 * time.Minute)
	end := now.AddDate(2, 0, 0) // mandate validity window — 2 years out; adjust to your actual policy

	result, err := h.Nomba.CreateDirectDebitMandate(services.CreateMandateInput{
		CustomerAccountNumber: req.AccountNumber,
		BankCode:              req.BankCode,
		CustomerName:          req.AccountName,
		CustomerAddress:       org.Name,
		CustomerAccountName:   req.AccountName,
		Amount:                sub.Amount,
		Frequency:             "MONTHLY",
		Narration:             fmt.Sprintf("%s subscription renewal", org.Name),
		CustomerPhoneNumber:   "", 
		MerchantReference:     merchantRef,
		StartDate:             start.Format("2006-01-02T15:04"),
		EndDate:               end.Format("2006-01-02T15:04"),
		CustomerEmail:         owner.Email,
		StartImmediately:      true,
	})
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "mandate creation failed: " + err.Error()})
	}

	sub.MandateID = result.MandateID
	sub.MandateStatus = models.MandateStatusPending
	sub.MandateBankCode = req.BankCode
	if len(req.AccountNumber) >= 4 {
		sub.MandateAccountLast = req.AccountNumber[len(req.AccountNumber)-4:]
	}
	if err := database.DB.Save(&sub).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "mandate created on Nomba but failed to save locally — mandate_id: " + result.MandateID})
	}

	return c.JSON(fiber.Map{
		"mandate_id":  result.MandateID,
		"status":      "pending",
		"description": result.Description, // shows the customer the NIBSS token-payment instructions
	})
}