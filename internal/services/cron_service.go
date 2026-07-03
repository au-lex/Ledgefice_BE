package services

import (
	"fmt"
	"time"

	"github.com/ledgefice/internal/database"
	"github.com/ledgefice/internal/models"
)

// RetrySchedule defines how long to wait after each failed attempt before

var RetrySchedule = []time.Duration{
	24 * time.Hour,
	3 * 24 * time.Hour,
	7 * 24 * time.Hour,
}

// RenewalService owns the renewal + dunning cron logic. It depends on
// NombaService for actually charging, and on an EmailSender for dunning
// emails 
type RenewalService struct {
	Nomba  *NombaService
	Mailer EmailSender
}

type EmailSender interface {
	Send(to, subject, htmlBody string) error
}

// ProcessDueRenewals  handles three separate cohorts of
// subscriptions in one pass:
//  1. Subscriptions due for their first renewal attempt this cycle.
//  2. Subscriptions mid-retry, due for their next retry attempt.
//  3. Subscriptions that have exhausted all retries and need to be cancelled.
func (r *RenewalService) ProcessDueRenewals() error {
	now := time.Now()

	if err := r.processInitialRenewals(now); err != nil {
		return fmt.Errorf("initial renewals: %w", err)
	}
	if err := r.processRetries(now); err != nil {
		return fmt.Errorf("retries: %w", err)
	}
	if err := r.processExhaustedRetries(now); err != nil {
		return fmt.Errorf("exhausted retries: %w", err)
	}
	return nil
}

// ─── Cohort 1: first attempt this billing cycle ───────────────────────────────

func (r *RenewalService) processInitialRenewals(now time.Time) error {
	var due []models.Subscription
	if err := database.DB.
		Where("status = ? AND renews_at IS NOT NULL AND renews_at <= ? AND cancelled_at IS NULL AND retry_count = 0", models.SubscriptionStatusPaid, now).
		Find(&due).Error; err != nil {
		return err
	}

	for _, sub := range due {
		r.attemptCharge(sub, false)
	}
	return nil
}

// ─── Cohort 2: mid-retry, next attempt is due ─────────────────────────────────

func (r *RenewalService) processRetries(now time.Time) error {
	var due []models.Subscription
	if err := database.DB.
		Where("status = ? AND next_retry_at IS NOT NULL AND next_retry_at <= ? AND cancelled_at IS NULL AND retry_count > 0 AND retry_count < ?",
			models.SubscriptionStatusFailed, now, len(RetrySchedule)).
		Find(&due).Error; err != nil {
		return err
	}

	for _, sub := range due {
		r.attemptCharge(sub, true)
	}
	return nil
}

// ─── Cohort 3: exhausted retries → cancel ─────────────────────────────────────

func (r *RenewalService) processExhaustedRetries(now time.Time) error {
	var due []models.Subscription
	if err := database.DB.
		Where("status = ? AND retry_count >= ? AND cancelled_at IS NULL", models.SubscriptionStatusFailed, len(RetrySchedule)).
		Find(&due).Error; err != nil {
		return err
	}

	for _, sub := range due {
		sub.CancelledAt = &now
		sub.DunningStage = "cancelled"
		if err := database.DB.Save(&sub).Error; err != nil {
			fmt.Println("failed to mark subscription cancelled:", sub.ID, err)
			continue
		}
		r.sendCancellationEmail(sub)
	}
	return nil
}

// ─── Charge attempt (shared by initial + retry cohorts) ───────────────────────

func (r *RenewalService) attemptCharge(sub models.Subscription, isRetry bool) {
	var owner models.User
	var org models.Organization
	if err := database.DB.Where("id = ?", sub.OrganizationID).First(&org).Error; err != nil {
		fmt.Println("renewal: organization not found for subscription", sub.ID, err)
		return
	}
	if org.OwnerID != nil {
		database.DB.Where("id = ?", *org.OwnerID).First(&owner)
	}

	var chargeErr error
	var succeeded bool

	switch {
	case sub.MandateID != "" && sub.TokenKey == "":
		if sub.MandateStatus != models.MandateStatusActive {
			// Don't attempt a debit against a mandate that hasn't been
			// confirmed active yet (still pending NIBSS authentication, or
			// failed/rejected). 
			chargeErr = fmt.Errorf("mandate %s is not active (status=%s) — skipping debit attempt", sub.MandateID, sub.MandateStatus)
			break
		}
		result, err := r.Nomba.DebitMandate(sub.MandateID, sub.Amount)
		if err != nil {
			chargeErr = err
		} else if result.Status == "SUCCESS" {
			succeeded = true
		} else {
			chargeErr = fmt.Errorf("mandate debit returned status=%s message=%s", result.Status, result.Message)
		}

	case sub.TokenKey != "":
		newOrderRef := fmt.Sprintf("renew_%s_%d", sub.OrganizationID.String()[:8], time.Now().Unix())
		_, err := r.Nomba.ChargeTokenizedCard(TokenizedChargeInput{
			OrderReference: newOrderRef,
			CustomerEmail:  owner.Email,
			Amount:         sub.Amount,
			Currency:       sub.Currency,
			TokenKey:       sub.TokenKey,
		})
		if err != nil {
			chargeErr = err
		} else {
	
			succeeded = true
		}

	default:
		chargeErr = fmt.Errorf("subscription %s has neither TokenKey nor MandateID", sub.ID)
	}

	now := time.Now()

	if succeeded {
		sub.Status = models.SubscriptionStatusPaid
		sub.PaidAt = &now
		nextRenewal := now.AddDate(0, 1, 0) // monthly cycle
		sub.RenewsAt = &nextRenewal
		sub.RetryCount = 0
		sub.NextRetryAt = nil
		sub.DunningStage = ""
		sub.LastRetryError = ""
		if err := database.DB.Save(&sub).Error; err != nil {
			fmt.Println("renewal: failed to save success state for", sub.ID, err)
			return
		}
		if isRetry {
			r.sendRenewalRecoveredEmail(sub, owner)
		}
		return
	}

	// Failed — advance retry state in place.
	sub.Status = models.SubscriptionStatusFailed
	sub.LastRetryError = chargeErr.Error()
	sub.RetryCount++

	if sub.RetryCount <= len(RetrySchedule) {
		wait := RetrySchedule[sub.RetryCount-1]
		next := now.Add(wait)
		sub.NextRetryAt = &next
		sub.DunningStage = fmt.Sprintf("retry_%d", sub.RetryCount)
	}

	if err := database.DB.Save(&sub).Error; err != nil {
		fmt.Println("renewal: failed to save failure state for", sub.ID, err)
		return
	}

	r.sendDunningEmail(sub, owner)
}

// ─── Emails ────────────────────────────────────────────────────────────────

func (r *RenewalService) sendDunningEmail(sub models.Subscription, owner models.User) {
	if r.Mailer == nil || owner.Email == "" {
		return
	}

	var subject, body string
	switch sub.DunningStage {
	case "retry_1":
		subject = "Your payment didn't go through"
		body = fmt.Sprintf(`<p>Hi,</p><p>We tried to renew your subscription (%.2f %s) but the payment failed. We'll try again in 24 hours — no action needed if this resolves itself (e.g. your card was temporarily declined).</p><p>If your card has expired or changed, please update your payment method as soon as possible to avoid interruption.</p>`, sub.Amount, sub.Currency)
	case "retry_2":
		subject = "Second attempt failed — please check your payment method"
		body = fmt.Sprintf(`<p>Hi,</p><p>We tried again to renew your subscription (%.2f %s) and it failed a second time. We'll make one more attempt in a few days, but please update your card or bank mandate soon to avoid your subscription being cancelled.</p>`, sub.Amount, sub.Currency)
	case "retry_3":
		subject = "Final attempt failed — your subscription will be cancelled soon"
		body = fmt.Sprintf(`<p>Hi,</p><p>We were unable to renew your subscription (%.2f %s) after several attempts. This is our final scheduled retry. If it fails again, your subscription will be cancelled and access suspended.</p><p>Please update your payment method now to avoid interruption.</p>`, sub.Amount, sub.Currency)
	default:
		return
	}

	if err := r.Mailer.Send(owner.Email, subject, body); err != nil {
		fmt.Println("failed to send dunning email:", err)
	}
}

func (r *RenewalService) sendRenewalRecoveredEmail(sub models.Subscription, owner models.User) {
	if r.Mailer == nil || owner.Email == "" {
		return
	}
	subject := "Payment successful — your subscription is active"
	body := fmt.Sprintf(`<p>Hi,</p><p>Good news — your payment of %.2f %s went through and your subscription is active again.</p>`, sub.Amount, sub.Currency)
	if err := r.Mailer.Send(owner.Email, subject, body); err != nil {
		fmt.Println("failed to send recovery email:", err)
	}
}

func (r *RenewalService) sendCancellationEmail(sub models.Subscription) {
	if r.Mailer == nil {
		return
	}
	var org models.Organization
	if err := database.DB.Where("id = ?", sub.OrganizationID).First(&org).Error; err != nil {
		return
	}
	var owner models.User
	if org.OwnerID != nil {
		database.DB.Where("id = ?", *org.OwnerID).First(&owner)
	}
	if owner.Email == "" {
		return
	}

	subject := "Your subscription has been cancelled"
	body := `<p>Hi,</p><p>After several failed payment attempts, your subscription has been cancelled and access has been suspended. You can resubscribe at any time by updating your payment method and starting a new subscription.</p>`
	if err := r.Mailer.Send(owner.Email, subject, body); err != nil {
		fmt.Println("failed to send cancellation email:", err)
	}
}