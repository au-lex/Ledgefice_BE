package database

import (
    "fmt"
    "log"
    "os"

    "gorm.io/driver/postgres"
    "gorm.io/gorm"
    "gorm.io/gorm/logger"
)

var DB *gorm.DB

func Connect() error {
    var dsn string
    
    // Try to use DATABASE_URL first
    dsn = os.Getenv("DATABASE_URL")
    

    if dsn == "" {
        host := os.Getenv("DB_HOST")
        user := os.Getenv("DB_USER")
        password := os.Getenv("DB_PASSWORD")
        dbname := os.Getenv("DB_NAME")
        port := os.Getenv("DB_PORT")
        
        if host == "" || user == "" || password == "" || dbname == "" || port == "" {
            return fmt.Errorf("database configuration not provided: either set DATABASE_URL or all of DB_HOST, DB_USER, DB_PASSWORD, DB_NAME, and DB_PORT")
        }
        
        dsn = fmt.Sprintf(
            "host=%s user=%s password=%s dbname=%s port=%s sslmode=disable TimeZone=UTC",
            host, user, password, dbname, port,
        )
        log.Println("Using individual database environment variables")
    } else {
        log.Println("Using DATABASE_URL")
    }

    var err error
    DB, err = gorm.Open(postgres.Open(dsn), &gorm.Config{
        Logger: logger.Default.LogMode(logger.Info),
    })

    if err != nil {
        return fmt.Errorf("failed to connect to database: %w", err)
    }

    log.Println("Database connected successfully")
    return nil
}

func Close() error {
    sqlDB, err := DB.DB()
    if err != nil {
        return err
    }
    return sqlDB.Close()
}