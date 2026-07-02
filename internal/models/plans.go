package models

import "github.com/google/uuid"






type PlanType string

const (
	PlanStarter    PlanType = "starter"
	PlanBusiness   PlanType = "business"
	PlanEnterprise PlanType = "enterprise"
)

type PlanFeatures struct {
	MultiStepApprovals     bool `json:"multi_step_approvals"`
	DepartmentPermissions  bool `json:"department_permissions"`
	FullReportingDashboard bool `json:"full_reporting_dashboard"`
	AuditLogExport         bool `json:"audit_log_export"`
	PrioritySupport        bool `json:"priority_support"`
}

type PlanConfig struct {
	Name           string       `json:"name"`
	MaxDepartments int          `json:"max_departments"` // -1 = unlimited
	MaxUsers       int          `json:"max_users"`       // -1 = unlimited
	MonthlyPrice   int          `json:"monthly_price"`   // kobo
	YearlyPrice    int          `json:"yearly_price"`    // kobo, per month equivalent
	Features       PlanFeatures `json:"features"`
}

var PlanConfigs = map[PlanType]PlanConfig{
	PlanStarter: {
		Name:           "Starter",
		MaxDepartments: 3,
		MaxUsers:       15,
		MonthlyPrice:   10000,
		YearlyPrice:    1200000,
		Features: PlanFeatures{
			MultiStepApprovals:     false,
			DepartmentPermissions:  false,
			FullReportingDashboard: false,
			AuditLogExport:         false,
			PrioritySupport:        false,
		},
	},
	PlanBusiness: {
		Name:           "Business",
		MaxDepartments: 15,
		MaxUsers:       150,
		MonthlyPrice:   1500,
		YearlyPrice:    3600000,
		Features: PlanFeatures{
			MultiStepApprovals:     true,
			DepartmentPermissions:  true,
			FullReportingDashboard: true,
			AuditLogExport:         true,
			PrioritySupport:        false,
		},
	},
	PlanEnterprise: {
		Name:           "Enterprise",
		MaxDepartments: -1,
		MaxUsers:       -1,
		MonthlyPrice:   0,
		YearlyPrice:    0,
		Features: PlanFeatures{
			MultiStepApprovals:     true,
			DepartmentPermissions:  true,
			FullReportingDashboard: true,
			AuditLogExport:         true,
			PrioritySupport:        true,
		},
	},
}

func GetPlanConfig(p PlanType) PlanConfig {
	if cfg, ok := PlanConfigs[p]; ok {
		return cfg
	}
	return PlanConfigs[PlanStarter]
}

type Organization struct {
	Base
	Name            string     `gorm:"uniqueIndex;not null" json:"name"`
	LogoURL         string     `json:"logo_url"`
	NumberOfWorkers int        `json:"number_of_workers"`
	Plan            PlanType   `gorm:"type:varchar(20);default:'starter'" json:"plan"`
	OwnerID         *uuid.UUID `gorm:"type:uuid" json:"owner_id"`
}
