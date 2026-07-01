package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
)

const resendAPIURL = "https://api.resend.com/emails"

type EmailClient struct {
	apiKey    string
	fromEmail string
	appURL    string
}

func NewEmailClient() *EmailClient {
	return &EmailClient{
		apiKey:    os.Getenv("RESEND_API_KEY"),
		fromEmail: os.Getenv("RESEND_FROM_EMAIL"),
		appURL:    os.Getenv("APP_URL"),
	}
}

type sendRequest struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Html    string   `json:"html"`
}

type errorResponse struct {
	Message string `json:"message"`
	Name    string `json:"name"`
}

func (c *EmailClient) Send(to, subject, html string) error {
	if c.apiKey == "" {
		return fmt.Errorf("RESEND_API_KEY is not set")
	}
	if c.fromEmail == "" {
		return fmt.Errorf("RESEND_FROM_EMAIL is not set")
	}

	payload, err := json.Marshal(sendRequest{
		From:    c.fromEmail,
		To:      []string{to},
		Subject: subject,
		Html:    html,
	})
	if err != nil {
		return fmt.Errorf("failed to marshal email payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, resendAPIURL, bytes.NewBuffer(payload))
	if err != nil {
		return fmt.Errorf("failed to build email request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send email: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	var errResp errorResponse
	_ = json.NewDecoder(resp.Body).Decode(&errResp)
	return fmt.Errorf("resend api error (%d): %s", resp.StatusCode, errResp.Message)
}

// ─── Shared layout helpers ────────────────────────────────────────────────────

func emailHeader(orgName string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
</head>
<body style="margin:0;padding:0;background:#09090b;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;">
  <table width="100%%" cellpadding="0" cellspacing="0" style="background:#09090b;padding:40px 16px;">
    <tr>
      <td align="center">
        <table width="100%%" cellpadding="0" cellspacing="0" style="max-width:520px;">

          <!-- Wordmark -->
          <tr>
            <td style="padding-bottom:28px;">
              <span style="font-size:15px;font-weight:700;color:#f4f4f5;letter-spacing:-0.5px;">Ledgefice</span>
              <span style="font-size:13px;color:#52525b;margin-left:6px;">· %s</span>
            </td>
          </tr>

          <!-- Card open -->
          <tr>
            <td style="background:#18181b;border:1px solid #27272a;border-radius:16px;padding:36px 32px;">
`, orgName)
}

func emailFooter() string {
	return `
            </td>
          </tr>

          <!-- Footer -->
          <tr>
            <td style="padding-top:24px;">
              <p style="margin:0;font-size:11px;color:#3f3f46;line-height:1.6;">
                © Ledgefice · Voucher Management System<br/>
                You're receiving this because you have an account on Ledgefice.
              </p>
            </td>
          </tr>

        </table>
      </td>
    </tr>
  </table>
</body>
</html>`
}

func metaRow(label, value string) string {
	return fmt.Sprintf(`
                <tr>
                  <td style="padding:14px 18px;border-bottom:1px solid #27272a;">
                    <p style="margin:0 0 3px;font-size:10px;font-weight:500;color:#52525b;text-transform:uppercase;letter-spacing:0.8px;">%s</p>
                    <p style="margin:0;font-size:13px;color:#e4e4e7;font-weight:500;">%s</p>
                  </td>
                </tr>`, label, value)
}

func ctaButton(href, label string) string {
	return fmt.Sprintf(`
              <table cellpadding="0" cellspacing="0">
                <tr>
                  <td style="background:#f4f4f5;border-radius:10px;">
                    <a href="%s" style="display:inline-block;padding:12px 28px;font-size:13px;font-weight:600;color:#09090b;text-decoration:none;">
                      %s →
                    </a>
                  </td>
                </tr>
              </table>`, href, label)
}

func statusBadge(label, bg, color string) string {
	return fmt.Sprintf(`<span style="display:inline-block;padding:4px 12px;border-radius:999px;background:%s;color:%s;font-size:11px;font-weight:600;letter-spacing:0.5px;">%s</span>`,
		bg, color, label)
}

func divider() string {
	return `<tr><td style="padding:0;height:1px;background:#27272a;margin:20px 0;"></td></tr>`
}

// ─── Templates ────────────────────────────────────────────────────────────────

// SendWelcome fires the user invite email with their credentials and login link.
func (c *EmailClient) SendWelcome(toEmail, fullName, password, orgName, department string) error {
	loginURL := c.appURL + "/login"

	html := emailHeader(orgName) + fmt.Sprintf(`
              <p style="margin:0 0 6px;font-size:11px;font-weight:600;color:#71717a;text-transform:uppercase;letter-spacing:1px;">You're invited</p>
              <h1 style="margin:0 0 10px;font-size:22px;font-weight:700;color:#f4f4f5;line-height:1.2;">Welcome, %s</h1>
              <p style="margin:0 0 28px;font-size:13px;color:#71717a;line-height:1.7;">
                Your account has been created on <strong style="color:#a1a1aa;">%s</strong>. Use the credentials below to sign in — you can update your password after your first login.
              </p>

              <!-- Credentials -->
              <table width="100%%" cellpadding="0" cellspacing="0" style="background:#09090b;border:1px solid #27272a;border-radius:10px;margin-bottom:28px;">
                %s
                %s
                %s
              </table>

              %s

              <p style="margin:24px 0 0;font-size:12px;color:#3f3f46;line-height:1.6;">
                If you weren't expecting this, you can safely ignore it. This invite was sent by your organisation admin.
              </p>`,
		fullName, orgName,
		metaRow("Email", toEmail),
		metaRow("Temporary Password", fmt.Sprintf(`<span style="font-family:monospace;letter-spacing:1px;">%s</span>`, password)),
		metaRow("Department", department),
		ctaButton(loginURL, "Log in to Ledgefice"),
	) + emailFooter()

	return c.Send(toEmail, "You've been added to "+orgName+" on Ledgefice", html)
}

// SendPasswordReset sends a password reset link.
func (c *EmailClient) SendPasswordReset(toEmail, fullName, resetToken string) error {
	resetURL := fmt.Sprintf("%s/reset-password?token=%s", c.appURL, resetToken)

	html := emailHeader("Account Security") + fmt.Sprintf(`
              <p style="margin:0 0 6px;font-size:11px;font-weight:600;color:#71717a;text-transform:uppercase;letter-spacing:1px;">Password Reset</p>
              <h1 style="margin:0 0 10px;font-size:22px;font-weight:700;color:#f4f4f5;line-height:1.2;">Reset your password</h1>
              <p style="margin:0 0 28px;font-size:13px;color:#71717a;line-height:1.7;">
                Hi %s, we received a request to reset the password for your Ledgefice account. Click the button below — this link expires in <strong style="color:#a1a1aa;">30 minutes</strong>.
              </p>

              %s

              <p style="margin:24px 0 0;font-size:12px;color:#3f3f46;line-height:1.6;">
                If you didn't request a password reset, you can safely ignore this email. Your password won't change.
              </p>`,
		fullName,
		ctaButton(resetURL, "Reset Password"),
	) + emailFooter()

	return c.Send(toEmail, "Reset your Ledgefice password", html)
}

// SendVoucherSubmitted notifies the first approver a voucher is awaiting their action.
func (c *EmailClient) SendVoucherSubmitted(
	toEmail, approverName, submitterName, voucherRef, voucherType, amount, orgName string,
) error {
	reviewURL := fmt.Sprintf("%s/vouchers/%s", c.appURL, voucherRef)

	html := emailHeader(orgName) + fmt.Sprintf(`
              <p style="margin:0 0 6px;font-size:11px;font-weight:600;color:#71717a;text-transform:uppercase;letter-spacing:1px;">Action Required</p>
              <h1 style="margin:0 0 10px;font-size:22px;font-weight:700;color:#f4f4f5;line-height:1.2;">Voucher awaiting your approval</h1>
              <p style="margin:0 0 24px;font-size:13px;color:#71717a;line-height:1.7;">
                Hi %s, <strong style="color:#a1a1aa;">%s</strong> has submitted a voucher that requires your review.
              </p>

              <!-- Voucher meta -->
              <table width="100%%" cellpadding="0" cellspacing="0" style="background:#09090b;border:1px solid #27272a;border-radius:10px;margin-bottom:28px;">
                %s
                %s
                %s
                <tr>
                  <td style="padding:14px 18px;">
                    <p style="margin:0 0 3px;font-size:10px;font-weight:500;color:#52525b;text-transform:uppercase;letter-spacing:0.8px;">Status</p>
                    <p style="margin:0;">%s</p>
                  </td>
                </tr>
              </table>

              %s

              <p style="margin:24px 0 0;font-size:12px;color:#3f3f46;line-height:1.6;">
                You can approve, query, or decline this voucher from your dashboard.
              </p>`,
		approverName, submitterName,
		metaRow("Reference", voucherRef),
		metaRow("Type", voucherType),
		metaRow("Amount", amount),
		statusBadge("Awaiting Approval", "#1c1917", "#fbbf24"),
		ctaButton(reviewURL, "Review Voucher"),
	) + emailFooter()

	return c.Send(toEmail, fmt.Sprintf("Voucher %s awaiting your approval", voucherRef), html)
}

// SendVoucherApproved notifies the requester their voucher has been fully approved.
func (c *EmailClient) SendVoucherApproved(
	toEmail, requesterName, voucherRef, voucherType, amount, orgName string,
) error {
	viewURL := fmt.Sprintf("%s/vouchers/%s", c.appURL, voucherRef)

	html := emailHeader(orgName) + fmt.Sprintf(`
              <p style="margin:0 0 6px;font-size:11px;font-weight:600;color:#71717a;text-transform:uppercase;letter-spacing:1px;">Approved</p>
              <h1 style="margin:0 0 10px;font-size:22px;font-weight:700;color:#f4f4f5;line-height:1.2;">Your voucher has been approved</h1>
              <p style="margin:0 0 24px;font-size:13px;color:#71717a;line-height:1.7;">
                Hi %s, your voucher has passed all approval stages and is now closed.
              </p>

              <table width="100%%" cellpadding="0" cellspacing="0" style="background:#09090b;border:1px solid #27272a;border-radius:10px;margin-bottom:28px;">
                %s
                %s
                %s
                <tr>
                  <td style="padding:14px 18px;">
                    <p style="margin:0 0 3px;font-size:10px;font-weight:500;color:#52525b;text-transform:uppercase;letter-spacing:0.8px;">Status</p>
                    <p style="margin:0;">%s</p>
                  </td>
                </tr>
              </table>

              %s

              <p style="margin:24px 0 0;font-size:12px;color:#3f3f46;line-height:1.6;">
                This voucher has been recorded against your department's spend and is fully auditable.
              </p>`,
		requesterName,
		metaRow("Reference", voucherRef),
		metaRow("Type", voucherType),
		metaRow("Amount", amount),
		statusBadge("Approved", "#052e16", "#6ee7b7"),
		ctaButton(viewURL, "View Voucher"),
	) + emailFooter()

	return c.Send(toEmail, fmt.Sprintf("Voucher %s has been approved", voucherRef), html)
}

// SendVoucherQueried notifies the requester their voucher has been queried and needs a response.
func (c *EmailClient) SendVoucherQueried(
	toEmail, requesterName, approverName, voucherRef, voucherType, queryNote, orgName string,
) error {
	replyURL := fmt.Sprintf("%s/vouchers/%s", c.appURL, voucherRef)

	html := emailHeader(orgName) + fmt.Sprintf(`
              <p style="margin:0 0 6px;font-size:11px;font-weight:600;color:#71717a;text-transform:uppercase;letter-spacing:1px;">Query</p>
              <h1 style="margin:0 0 10px;font-size:22px;font-weight:700;color:#f4f4f5;line-height:1.2;">Your voucher has been queried</h1>
              <p style="margin:0 0 24px;font-size:13px;color:#71717a;line-height:1.7;">
                Hi %s, <strong style="color:#a1a1aa;">%s</strong> has raised a query on your voucher <strong style="color:#a1a1aa;">%s</strong>. Please review and respond.
              </p>

              <!-- Query note -->
              <table width="100%%" cellpadding="0" cellspacing="0" style="background:#09090b;border:1px solid #27272a;border-radius:10px;margin-bottom:28px;">
                %s
                <tr>
                  <td style="padding:14px 18px;border-bottom:1px solid #27272a;">
                    <p style="margin:0 0 3px;font-size:10px;font-weight:500;color:#52525b;text-transform:uppercase;letter-spacing:0.8px;">Status</p>
                    <p style="margin:0;">%s</p>
                  </td>
                </tr>
                <tr>
                  <td style="padding:14px 18px;">
                    <p style="margin:0 0 6px;font-size:10px;font-weight:500;color:#52525b;text-transform:uppercase;letter-spacing:0.8px;">Query note</p>
                    <p style="margin:0;font-size:13px;color:#e4e4e7;line-height:1.6;font-style:italic;">"%s"</p>
                  </td>
                </tr>
              </table>

              %s

              <p style="margin:24px 0 0;font-size:12px;color:#3f3f46;line-height:1.6;">
                Once you respond, the voucher will re-enter the approval chain from where it was queried.
              </p>`,
		requesterName, approverName, voucherRef,
		metaRow("Reference", voucherRef),
		statusBadge("Queried", "#1c1917", "#fbbf24"),
		queryNote,
		ctaButton(replyURL, "Respond to Query"),
	) + emailFooter()

	return c.Send(toEmail, fmt.Sprintf("Query raised on voucher %s", voucherRef), html)
}

// SendVoucherDeclined notifies the requester their voucher has been declined.
func (c *EmailClient) SendVoucherDeclined(
	toEmail, requesterName, approverName, voucherRef, voucherType, reason, orgName string,
) error {
	viewURL := fmt.Sprintf("%s/vouchers/%s", c.appURL, voucherRef)

	html := emailHeader(orgName) + fmt.Sprintf(`
              <p style="margin:0 0 6px;font-size:11px;font-weight:600;color:#71717a;text-transform:uppercase;letter-spacing:1px;">Declined</p>
              <h1 style="margin:0 0 10px;font-size:22px;font-weight:700;color:#f4f4f5;line-height:1.2;">Your voucher has been declined</h1>
              <p style="margin:0 0 24px;font-size:13px;color:#71717a;line-height:1.7;">
                Hi %s, <strong style="color:#a1a1aa;">%s</strong> has declined your voucher <strong style="color:#a1a1aa;">%s</strong>.
              </p>

              <table width="100%%" cellpadding="0" cellspacing="0" style="background:#09090b;border:1px solid #27272a;border-radius:10px;margin-bottom:28px;">
                %s
                %s
                <tr>
                  <td style="padding:14px 18px;border-bottom:1px solid #27272a;">
                    <p style="margin:0 0 3px;font-size:10px;font-weight:500;color:#52525b;text-transform:uppercase;letter-spacing:0.8px;">Status</p>
                    <p style="margin:0;">%s</p>
                  </td>
                </tr>
                <tr>
                  <td style="padding:14px 18px;">
                    <p style="margin:0 0 6px;font-size:10px;font-weight:500;color:#52525b;text-transform:uppercase;letter-spacing:0.8px;">Reason</p>
                    <p style="margin:0;font-size:13px;color:#e4e4e7;line-height:1.6;font-style:italic;">"%s"</p>
                  </td>
                </tr>
              </table>

              %s

              <p style="margin:24px 0 0;font-size:12px;color:#3f3f46;line-height:1.6;">
                If you believe this was an error, contact your department head or raise a new voucher with the necessary corrections.
              </p>`,
		requesterName, approverName, voucherRef,
		metaRow("Reference", voucherRef),
		metaRow("Type", voucherType),
		statusBadge("Declined", "#1f0f0f", "#fca5a5"),
		reason,
		ctaButton(viewURL, "View Voucher"),
	) + emailFooter()

	return c.Send(toEmail, fmt.Sprintf("Voucher %s has been declined", voucherRef), html)
}