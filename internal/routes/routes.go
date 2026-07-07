package routes

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/ledgefice/internal/handlers"
	"github.com/ledgefice/internal/middleware"
	"github.com/ledgefice/internal/models"
	"github.com/ledgefice/internal/services"
)

func RegisterRoutes(app *fiber.App, jwtSecret string, imgSvc *services.ImageService, emailSvc *services.EmailClient, nombaSvc *services.NombaService, renewalSvc *services.RenewalService) {

	api := app.Group("/api/v1")

	// ─── Public ─────────────────────────────────────────────────────────────

	api.Get("/plans", func(c *fiber.Ctx) error {
		return c.JSON(models.PlanConfigs)
	})

	onboarding := &handlers.OnboardingHandler{Images: imgSvc, Nomba: nombaSvc}
	api.Post("/onboarding/setup", onboarding.Setup)

	auth := &handlers.AuthHandler{JWTSecret: jwtSecret, JWTExpiresIn: "24h", Images: imgSvc}
	api.Post("/auth/login", auth.Login)

	// Nomba payment callback (browser redirect) + webhook (server-to-server)
	payments := &handlers.PaymentHandler{Nomba: nombaSvc}
	api.Get("/payments/nomba/callback", payments.NombaCallback)
	api.Post("/payments/nomba/webhook", payments.NombaWebhook)

	subs := &handlers.SubscriptionHandler{Nomba: nombaSvc}
	api.Get("/subscriptions/:ref/status", subs.Status)

	// ─── Protected ──────────────────────────────────────────────────────────
	guard := middleware.Protected(jwtSecret)

	api.Get("/auth/me", guard, auth.Me)
	api.Put("/auth/me", guard, auth.UpdateMe)

	api.Get("/organizations/me", guard, onboarding.GetMe)
	api.Put("/organizations/me", guard, onboarding.UpdateMe)

	// Logged-in org's own saved card — lookup + delete. Reg
	api.Get("/subscriptions/me/token", guard, subs.MyToken)
	api.Delete("/subscriptions/me/token", guard, subs.DeleteMyToken)
	api.Get("/subscriptions/me/history", guard, subs.MyHistory)
	api.Get("/subscriptions/me/plan", guard, subs.MyPlan)
	api.Post("/subscriptions/me/upgrade", guard, subs.Upgrade)

	api.Get("/subscriptions/:ref/token", subs.Token)
	// Renew a subscription using its saved tokenized card
	api.Post("/subscriptions/:id/renew", guard, subs.Renew)
	// List every saved tokenized card across subscriptions — admin/debug view.
	api.Get("/subscriptions/tokens", subs.ListTokens)

	// Direct Debit mandate flow — fallback recurring billing for orgs that paid
	// via bank_transfer and have no tokenized card to renew against.
	mandates := &handlers.MandateHandler{Nomba: nombaSvc}
	api.Get("/mandates/banks", guard, mandates.ListBanks)
	api.Post("/mandates/lookup-account", guard, mandates.LookupAccount)
	api.Post("/mandates", guard, mandates.CreateMandate)
	api.Get("/mandates/:mandateId/status", guard, mandates.GetMandateStatus)
	api.Post("/mandates/reset", guard, mandates.ResetMandate)

	// Users
	users := &handlers.UserHandler{Email: emailSvc}
	api.Post("/users", guard, middleware.RequirePermission(models.PermCanManageUsers), users.Create)
	api.Get("/users", guard, middleware.RequirePermission(models.PermCanManageUsers), users.List)
	api.Get("/users/:id", guard, middleware.RequirePermission(models.PermCanManageUsers), users.Get)
	api.Put("/users/:id", guard, middleware.RequirePermission(models.PermCanManageUsers), users.Update)
	api.Delete("/users/:id", guard, middleware.RequirePermission(models.PermCanManageUsers), users.Delete)
	api.Patch("/users/:id/status", guard, middleware.RequirePermission(models.PermCanManageUsers), users.SetStatus)

	// Departments
	depts := &handlers.DepartmentHandler{}
	api.Get("/departments", guard, middleware.RequirePermission(models.PermCanViewDepartments), depts.List)
	api.Get("/departments/:id", guard, middleware.RequirePermission(models.PermCanViewDepartments), depts.Get)
	api.Post("/departments", guard, middleware.RequirePermission(models.PermCanCreateDepartments), depts.Create)
	api.Put("/departments/:id", guard, middleware.RequirePermission(models.PermCanEditDepartments), depts.Update)
	api.Delete("/departments/:id", guard, middleware.RequirePermission(models.PermCanDeleteDepartments), depts.Delete)

	// Voucher Types
	vtypes := &handlers.VoucherTypeHandler{}
	api.Get("/voucher-types", guard, middleware.RequirePermission(models.PermCanViewVoucherTypes), vtypes.List)
	api.Get("/voucher-types/:id", guard, middleware.RequirePermission(models.PermCanViewVoucherTypes), vtypes.Get)
	api.Post("/voucher-types", guard, middleware.RequirePermission(models.PermCanCreateVoucherTypes), vtypes.Create)
	api.Put("/voucher-types/:id", guard, middleware.RequirePermission(models.PermCanEditVoucherTypes), vtypes.Update)
	api.Delete("/voucher-types/:id", guard, middleware.RequirePermission(models.PermCanDeleteVoucherTypes), vtypes.Delete)

	// Approval Chains
	chains := &handlers.ApprovalChainHandler{}
	api.Get("/approval-chains", guard, middleware.RequirePermission(models.PermCanViewApprovalChains), chains.List)
	api.Get("/approval-chains/:id", guard, middleware.RequirePermission(models.PermCanViewApprovalChains), chains.Get)
	api.Post("/approval-chains", guard, middleware.RequirePermission(models.PermCanCreateApprovalChains), chains.Create)
	api.Put("/approval-chains/:id", guard, middleware.RequirePermission(models.PermCanEditApprovalChains), chains.Update)
	api.Delete("/approval-chains/:id", guard, middleware.RequirePermission(models.PermCanDeleteApprovalChains), chains.Delete)

	// Vouchers
	vouchers := &handlers.VoucherHandler{}
	api.Get("/vouchers", guard, middleware.RequirePermission(models.PermCanViewAll), vouchers.List)
	api.Get("/vouchers/submitted", guard, middleware.RequirePermission(models.PermCanApprove), vouchers.ListSubmitted)
	api.Get("/vouchers/my", guard, vouchers.ListMine)
	api.Get("/vouchers/:id", guard, middleware.RequirePermission(models.PermCanViewAll), vouchers.Get)
	api.Post("/vouchers", guard, middleware.RequirePermission(models.PermCanCreate), vouchers.Create)
	api.Delete("/vouchers/:id", guard, middleware.RequirePermission(models.PermCanCreate), vouchers.Delete)
	api.Post("/vouchers/:id/submit", guard, middleware.RequirePermission(models.PermCanCreate), vouchers.Submit)
	api.Post("/vouchers/:id/approve", guard, middleware.RequirePermission(models.PermCanApprove), vouchers.Approve)
	api.Post("/vouchers/:id/reject", guard, middleware.RequirePermission(models.PermCanApprove), vouchers.Reject)
	api.Delete("/vouchers/:id/duplicate-flag", guard, middleware.RequirePermission(models.PermCanDismissDuplicates), vouchers.DismissDuplicate)

	// Reports
	reports := &handlers.ReportsHandler{}
	api.Get("/reports/summary", guard, middleware.RequirePermission(models.PermCanViewReports), reports.Summary)
	api.Get("/reports/spend-over-time", guard, middleware.RequirePermission(models.PermCanViewReports), reports.SpendOverTime)
	api.Get("/reports/spend-by-dept", guard, middleware.RequirePermission(models.PermCanViewReports), reports.SpendByDept)
	api.Get("/reports/volume-by-type", guard, middleware.RequirePermission(models.PermCanViewReports), reports.VolumeByType)

	// Audit
	audit := &handlers.AuditHandler{}
	api.Get("/audit-logs", guard, middleware.RequirePermission(models.PermCanViewAuditLogs), audit.List)

	// ─── Admin ────────────────────────────────────────────────────────────────
	admin := &handlers.AdminHandler{Renewal: renewalSvc, JWTSecret: jwtSecret, JWTExpiresIn: 24 * time.Hour}
	api.Post("/admin/auth/login", admin.Login)

	adminGuard := middleware.RequireAdmin(jwtSecret)
	adminGroup := api.Group("/admin", adminGuard)
	adminGroup.Post("/renewals/run", admin.RunRenewalsNow)
	adminGroup.Get("/organizations", admin.ListOrganizations)
	adminGroup.Get("/organizations/:id", admin.GetOrganization)
	adminGroup.Put("/organizations/:id", admin.UpdateOrganization)
	adminGroup.Delete("/organizations/:id", admin.DeleteOrganization)

	// Health check
	app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})
}
