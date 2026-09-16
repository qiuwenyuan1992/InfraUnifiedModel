package repository

import (
	"UnifiedInfraTopology/pkg/log"
	"context"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

var logger *log.Logger

func TestNewDBUsesMainConfiguration(t *testing.T) {
	conf := viper.New()
	conf.Set("data.db.main.driver", "sqlite")
	conf.Set("data.db.main.dsn", ":memory:")
	db := repository.NewDB(conf, &log.Logger{Logger: zap.NewNop()})
	sqlDB, err := db.DB()
	if !assert.NoError(t, err) {
		return
	}
	t.Cleanup(func() { assert.NoError(t, sqlDB.Close()) })
	assert.NoError(t, sqlDB.Ping())
	var database struct{ File string }
	assert.NoError(t, db.Raw("PRAGMA database_list").Scan(&database).Error)
	assert.Empty(t, database.File)
}

func setupRepository(t *testing.T) (repository.UserRepository, sqlmock.Sqlmock) {
	mockDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}

	db, err := gorm.Open(mysql.New(mysql.Config{
		Conn:                      mockDB,
		SkipInitializeWithVersion: true,
	}), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open gorm connection: %v", err)
	}

	//rdb, _ := redismock.NewClientMock()

	repo := repository.NewRepository(logger, db)
	userRepo := repository.NewUserRepository(repo)

	return userRepo, mock
}

func TestUserRepository_Create(t *testing.T) {
	userRepo, mock := setupRepository(t)

	ctx := context.Background()
	user := &model.User{
		Id:        1,
		UserId:    "123",
		Nickname:  "Test",
		Password:  "password",
		Email:     "test@example.com",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `users`").
		WithArgs(user.UserId, user.Nickname, user.Password, user.Email, user.CreatedAt, user.UpdatedAt, user.DeletedAt, user.Id).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := userRepo.Create(ctx, user)
	assert.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepository_Update(t *testing.T) {
	userRepo, mock := setupRepository(t)

	ctx := context.Background()
	user := &model.User{
		Id:        1,
		UserId:    "123",
		Nickname:  "Test",
		Password:  "password",
		Email:     "test@example.com",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `users`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := userRepo.Update(ctx, user)
	assert.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepository_GetById(t *testing.T) {
	userRepo, mock := setupRepository(t)

	ctx := context.Background()
	userId := "123"

	rows := sqlmock.NewRows([]string{"id", "user_id", "username", "nickname", "password", "email", "created_at", "updated_at"}).
		AddRow(1, "123", "test", "Test", "password", "test@example.com", time.Now(), time.Now())
	mock.ExpectQuery("SELECT \\* FROM `users`").WillReturnRows(rows)

	user, err := userRepo.GetByID(ctx, userId)
	assert.NoError(t, err)
	assert.NotNil(t, user)
	assert.Equal(t, "123", user.UserId)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepository_GetByUsername(t *testing.T) {
	userRepo, mock := setupRepository(t)

	ctx := context.Background()
	email := "test@example.com"

	rows := sqlmock.NewRows([]string{"id", "user_id", "username", "nickname", "password", "email", "created_at", "updated_at"}).
		AddRow(1, "123", "test", "Test", "password", "test@example.com", time.Now(), time.Now())
	mock.ExpectQuery("SELECT \\* FROM `users`").WillReturnRows(rows)

	user, err := userRepo.GetByEmail(ctx, email)
	assert.NoError(t, err)
	assert.NotNil(t, user)
	assert.Equal(t, "test@example.com", user.Email)

	assert.NoError(t, mock.ExpectationsWereMet())
}
