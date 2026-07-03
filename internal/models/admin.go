package models

import (
	"golang.org/x/crypto/bcrypt"
)


type AdminUser struct {
	Base
	FirstName string `gorm:"not null" json:"first_name"`
	LastName  string `gorm:"not null" json:"last_name"`
	Email     string `gorm:"uniqueIndex;not null" json:"email"`
	Password  string `gorm:"not null" json:"-"`
	IsActive  bool   `gorm:"default:true" json:"is_active"`
}

func (a *AdminUser) HashPassword() error {
	hashed, err := bcrypt.GenerateFromPassword([]byte(a.Password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	a.Password = string(hashed)
	return nil
}

func (a *AdminUser) CheckPassword(plain string) error {
	return bcrypt.CompareHashAndPassword([]byte(a.Password), []byte(plain))
}