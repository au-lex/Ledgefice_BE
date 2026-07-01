package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ─── Base ─────────────────────────────────────────────────────────────────────

type Base struct {
	ID        uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// ─── User ─────────────────────────────────────────────────────────────────────

type UserStatus string

const (
	UserStatusActive    UserStatus = "active"
	UserStatusSuspended UserStatus = "suspended"
	UserStatusBlocked   UserStatus = "blocked"
)

type User struct {
	Base
	OrganizationID uuid.UUID     `gorm:"type:uuid;not null;index" json:"organization_id"`
	Organization   *Organization `gorm:"foreignKey:OrganizationID" json:"organization,omitempty"`
	Name           string        `gorm:"not null" json:"name"`
	Email          string        `gorm:"uniqueIndex;not null" json:"email"`
	Password       string        `gorm:"not null" json:"-"`
	DepartmentID   *uuid.UUID    `gorm:"type:uuid" json:"department_id"`
	Department     *Department   `gorm:"foreignKey:DepartmentID" json:"department,omitempty"`
	Status         UserStatus    `gorm:"type:varchar(20);default:'active'" json:"status"`
	LastLoginAt    *time.Time    `json:"last_login_at"`
}

// ─── Department ───────────────────────────────────────────────────────────────

type PermissionMap map[string]bool

type Department struct {
	Base
	OrganizationID uuid.UUID     `gorm:"type:uuid;not null;index" json:"organization_id"`
	Name           string        `gorm:"not null" json:"name"`
	Code           string        `gorm:"not null;size:6" json:"code"`
	IconKey        string        `gorm:"default:'Building'" json:"icon_key"`
	Permissions    PermissionMap `gorm:"serializer:json" json:"permissions,omitempty"`
	Users          []User        `gorm:"foreignKey:DepartmentID" json:"users,omitempty"`
	Vouchers       []Voucher     `gorm:"foreignKey:DepartmentID" json:"vouchers,omitempty"`
}

// ─── VoucherType & Custom Fields ─────────────────────────────────────────────

type FieldType string

const (
	FieldTypeText   FieldType = "text"
	FieldTypeNumber FieldType = "number"
	FieldTypeDate   FieldType = "date"
	FieldTypeFile   FieldType = "file"
)

type VoucherType struct {
	Base
	OrganizationID uuid.UUID     `gorm:"type:uuid;not null;index" json:"organization_id"`
	Name           string        `gorm:"not null" json:"name"`
	Description    string        `json:"description"`
	CustomFields   []CustomField `gorm:"foreignKey:VoucherTypeID" json:"fields"`
}

type CustomField struct {
	Base
	VoucherTypeID uuid.UUID `gorm:"type:uuid;not null;index" json:"voucher_type_id"`
	Label         string    `gorm:"not null" json:"label"`
	FieldType     FieldType `gorm:"type:varchar(20);not null" json:"type"`
	SortOrder     int       `gorm:"default:0" json:"sort_order"`
}

// ─── Approval Chain ───────────────────────────────────────────────────────────

type ApprovalChain struct {
	Base
	OrganizationID uuid.UUID    `gorm:"type:uuid;not null;index" json:"organization_id"`
	VoucherTypeID  uuid.UUID    `gorm:"type:uuid;not null" json:"voucher_type_id"`
	VoucherType    *VoucherType `gorm:"foreignKey:VoucherTypeID" json:"voucher_type,omitempty"`
	Tiers          []AmountTier `gorm:"foreignKey:ApprovalChainID;constraint:OnDelete:CASCADE" json:"tiers"`
}

type AmountTier struct {
	Base
	ApprovalChainID uuid.UUID      `gorm:"type:uuid;not null;index" json:"approval_chain_id"`
	Label           string         `gorm:"not null" json:"label"`
	MinAmount       float64        `gorm:"not null;default:0" json:"min_amount"`
	MaxAmount       *float64       `json:"max_amount"`
	SortOrder       int            `gorm:"default:0" json:"sort_order"`
	Steps           []ApproverStep `gorm:"foreignKey:AmountTierID;constraint:OnDelete:CASCADE" json:"steps"`
}

type ApproverStep struct {
	Base
	AmountTierID uuid.UUID   `gorm:"type:uuid;not null;index" json:"amount_tier_id"`
	DepartmentID uuid.UUID   `gorm:"type:uuid;not null" json:"department_id"`
	Department   *Department `gorm:"foreignKey:DepartmentID" json:"department,omitempty"`
	StepOrder    int         `gorm:"not null;default:0" json:"step_order"`
}

// ─── Voucher ─────────────────────────────────────────────────────────────────

type VoucherStatus string

const (
	VoucherStatusDraft    VoucherStatus = "draft"
	VoucherStatusPending  VoucherStatus = "pending"
	VoucherStatusApproved VoucherStatus = "approved"
	VoucherStatusRejected VoucherStatus = "rejected"
)

type Voucher struct {
	Base
	OrganizationID        uuid.UUID           `gorm:"type:uuid;not null;index" json:"organization_id"`
	Code                  string              `gorm:"uniqueIndex;not null" json:"code"`
	Amount                float64             `gorm:"not null;default:0" json:"amount"`
	Status                VoucherStatus       `gorm:"type:varchar(20);default:'draft'" json:"status"`
	Tier                  int                 `gorm:"default:1" json:"tier"`
	DepartmentID          uuid.UUID           `gorm:"type:uuid;not null;index" json:"department_id"`
	Department            *Department         `gorm:"foreignKey:DepartmentID" json:"department,omitempty"`
	VoucherTypeID         uuid.UUID           `gorm:"type:uuid;not null;index" json:"voucher_type_id"`
	VoucherType           *VoucherType        `gorm:"foreignKey:VoucherTypeID" json:"voucher_type,omitempty"`
	RaisedByID            uuid.UUID           `gorm:"type:uuid;not null;index" json:"raised_by_id"`
	RaisedBy              *User               `gorm:"foreignKey:RaisedByID" json:"raised_by,omitempty"`
	AmountTierID          *uuid.UUID          `gorm:"type:uuid" json:"amount_tier_id"`
	AmountTier            *AmountTier         `gorm:"foreignKey:AmountTierID" json:"amount_tier,omitempty"`
	CurrentApproverDeptID *uuid.UUID          `gorm:"type:uuid" json:"current_approver_dept_id"`
	CurrentApproverDept   *Department         `gorm:"foreignKey:CurrentApproverDeptID" json:"current_approver_dept,omitempty"`
	FieldValues           []VoucherFieldValue `gorm:"foreignKey:VoucherID;constraint:OnDelete:CASCADE" json:"field_values,omitempty"`
	ApprovalHistory       []ApprovalHistory   `gorm:"foreignKey:VoucherID;constraint:OnDelete:CASCADE" json:"approval_history,omitempty"`
	DuplicateFlag         *DuplicateFlag      `gorm:"foreignKey:VoucherID;constraint:OnDelete:CASCADE" json:"duplicate_flag,omitempty"`
}

type VoucherFieldValue struct {
	Base
	VoucherID     uuid.UUID    `gorm:"type:uuid;not null;index" json:"voucher_id"`
	CustomFieldID uuid.UUID    `gorm:"type:uuid;not null" json:"custom_field_id"`
	CustomField   *CustomField `gorm:"foreignKey:CustomFieldID" json:"field,omitempty"`
	Value         string       `json:"value"`
}

// ─── Approval History ─────────────────────────────────────────────────────────

type ApprovalAction string

const (
	ApprovalActionApproved ApprovalAction = "approved"
	ApprovalActionRejected ApprovalAction = "rejected"
	ApprovalActionPending  ApprovalAction = "pending"
)

type ApprovalHistory struct {
	Base
	VoucherID    uuid.UUID      `gorm:"type:uuid;not null;index" json:"voucher_id"`
	ActorID      *uuid.UUID     `gorm:"type:uuid" json:"actor_id"`
	Actor        *User          `gorm:"foreignKey:ActorID" json:"actor,omitempty"`
	DepartmentID uuid.UUID      `gorm:"type:uuid;not null" json:"department_id"`
	Department   *Department    `gorm:"foreignKey:DepartmentID" json:"department,omitempty"`
	Action       ApprovalAction `gorm:"type:varchar(20);not null" json:"action"`
	Comment      string         `json:"comment"`
	ActedAt      *time.Time     `json:"acted_at"`
}

// ─── Duplicate Flag ───────────────────────────────────────────────────────────

type DuplicateFlag struct {
	Base
	VoucherID   uuid.UUID  `gorm:"type:uuid;uniqueIndex;not null" json:"voucher_id"`
	IsDuplicate bool       `gorm:"default:false" json:"is_duplicate"`
	Reason      string     `json:"reason"`
	MatchRef    string     `json:"match_ref"`
	DismissedAt *time.Time `json:"dismissed_at"`
	DismissedBy *uuid.UUID `gorm:"type:uuid" json:"dismissed_by"`
}

// ─── Audit Log ────────────────────────────────────────────────────────────────

type AuditAction string

const (
	AuditActionCreate      AuditAction = "CREATE"
	AuditActionUpdate      AuditAction = "UPDATE"
	AuditActionDelete      AuditAction = "DELETE"
	AuditActionApprove     AuditAction = "APPROVE"
	AuditActionReject      AuditAction = "REJECT"
	AuditActionAuthSuccess AuditAction = "AUTH_SUCCESS"
	AuditActionAuthFailure AuditAction = "AUTH_FAILURE"
)

type AuditModule string

const (
	AuditModuleUsers       AuditModule = "Users"
	AuditModuleVouchers    AuditModule = "Vouchers"
	AuditModuleDepartments AuditModule = "Departments"
	AuditModuleWorkflows   AuditModule = "Workflows"
	AuditModuleSystem      AuditModule = "System"
)

type AuditLog struct {
	Base
	OrganizationID *uuid.UUID  `gorm:"type:uuid;index" json:"organization_id"`
	ActorID        *uuid.UUID  `gorm:"type:uuid" json:"actor_id"`
	Actor          *User       `gorm:"foreignKey:ActorID" json:"actor,omitempty"`
	ActorName      string      `json:"actor_name"`
	ActorEmail     string      `json:"actor_email"`
	Action         AuditAction `gorm:"type:varchar(30);not null" json:"action"`
	Module         AuditModule `gorm:"type:varchar(30);not null" json:"module"`
	ResourceID     string      `json:"resource_id"`
	Description    string      `json:"description"`
	IPAddress      string      `json:"ip_address"`
	UserAgent      string      `json:"user_agent"`
}

// ─── Preload Helpers ──────────────────────────────────────────────────────────

// SlimDept scopes a department preload to exclude the permissions column.
// Use on any voucher query — auth/login handlers load departments independently
// and are completely unaffected.
func SlimDept(db *gorm.DB) *gorm.DB {
	return db.Select("id, organization_id, name, code, icon_key, created_at, updated_at")
}

// SlimUser scopes a user preload to exclude heavy/sensitive columns.
func SlimUser(db *gorm.DB) *gorm.DB {
	return db.Select("id, organization_id, name, email, department_id, status, last_login_at, created_at, updated_at")
}