package models

// Permission keys — single source of truth for both storage and middleware checks.
const (
	// Vouchers
	PermCanCreate            = "can_create"
	PermCanApprove           = "can_approve"
	PermCanDismissDuplicates = "can_dismiss_duplicates"
	PermCanViewAll           = "can_view_all"
	PermCanViewAllVouchers   = "can_view_all_vouchers"
	PermCanViewReports       = "can_view_reports"

	// Voucher Types
	PermCanViewVoucherTypes   = "can_view_voucher_types"
	PermCanCreateVoucherTypes = "can_create_voucher_types"
	PermCanEditVoucherTypes   = "can_edit_voucher_types"
	PermCanDeleteVoucherTypes = "can_delete_voucher_types"
	PermCanManageVoucherTypes = "can_manage_voucher_types"

	// Billings
	PermCanViewBillings   = "can_view_billings"
	PermCanCreateBillings = "can_create_billings"
	PermCanEditBillings   = "can_edit_billings"
	PermCanDeleteBillings = "can_delete_billings"
	PermCanManageBillings = "can_manage_billings"

	// Approval Chains
	PermCanViewApprovalChains   = "can_view_approval_chains"
	PermCanCreateApprovalChains = "can_create_approval_chains"
	PermCanEditApprovalChains   = "can_edit_approval_chains"
	PermCanDeleteApprovalChains = "can_delete_approval_chains"

	// Departments
	PermCanViewDepartments   = "can_view_departments"
	PermCanCreateDepartments = "can_create_departments"
	PermCanEditDepartments   = "can_edit_departments"
	PermCanDeleteDepartments = "can_delete_departments"

	// Administration
	PermCanManageUsers     = "can_manage_users"
	PermCanConfigure       = "can_configure"
	PermCanViewAuditLogs   = "can_view_audit_logs"
	PermCanExportAuditLogs = "can_export_audit_logs"
)

// FullPermissions returns a PermissionMap with every permission flag set to true.
// Used when creating the org owner department during onboarding.
func FullPermissions() PermissionMap {
	return PermissionMap{
		PermCanCreate:               true,
		PermCanApprove:              true,
		PermCanDismissDuplicates:    true,
		PermCanViewAll:              true,
		PermCanViewAllVouchers:      true,
		PermCanViewReports:          true,
		PermCanViewVoucherTypes:     true,
		PermCanCreateVoucherTypes:   true,
		PermCanEditVoucherTypes:     true,
		PermCanDeleteVoucherTypes:   true,
		PermCanManageVoucherTypes:   true,
		PermCanViewBillings:         true,
		PermCanCreateBillings:       true,
		PermCanEditBillings:         true,
		PermCanDeleteBillings:       true,
		PermCanManageBillings:       true,
		PermCanViewApprovalChains:   true,
		PermCanCreateApprovalChains: true,
		PermCanEditApprovalChains:   true,
		PermCanDeleteApprovalChains: true,
		PermCanViewDepartments:      true,
		PermCanCreateDepartments:    true,
		PermCanEditDepartments:      true,
		PermCanDeleteDepartments:    true,
		PermCanManageUsers:          true,
		PermCanConfigure:            true,
		PermCanViewAuditLogs:        true,
		PermCanExportAuditLogs:      true,
	}
}