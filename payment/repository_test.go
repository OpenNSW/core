package payment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)

	gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	require.NoError(t, err)

	return gormDB, mock
}

func TestRepository_Create(t *testing.T) {
	db, mock := setupTestDB(t)
	repo := NewPaymentRepository(db)

	tx := &PaymentTransaction{
		ID:              "uuid-1",
		ReferenceNumber: "TNSW1",
		TaskID:          "task-1",
		GatewayID:       "govpay",
		Amount:          decimal.NewFromInt(1500),
		Currency:        "LKR",
		Status:          PaymentStatusPending,
		ExpiryDate:      time.Now().Add(time.Hour),
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO "payment_transactions"`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Create(context.Background(), tx))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRepository_GetByReferenceNumber(t *testing.T) {
	db, mock := setupTestDB(t)
	repo := NewPaymentRepository(db)

	t.Run("found", func(t *testing.T) {
		rows := sqlmock.NewRows([]string{"id", "reference_number", "status"}).
			AddRow("uuid-1", "TNSW1", PaymentStatusPending)
		mock.ExpectQuery(`SELECT \* FROM "payment_transactions" WHERE reference_number = \$1`).
			WillReturnRows(rows)

		res, err := repo.GetByReferenceNumber(context.Background(), "TNSW1")
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.Equal(t, "TNSW1", res.ReferenceNumber)
	})

	t.Run("not found returns nil,nil", func(t *testing.T) {
		mock.ExpectQuery(`SELECT \* FROM "payment_transactions" WHERE reference_number = \$1`).
			WillReturnError(gorm.ErrRecordNotFound)

		res, err := repo.GetByReferenceNumber(context.Background(), "UNKNOWN")
		require.NoError(t, err, "not found must be (nil, nil), not an error")
		assert.Nil(t, res)
	})

	t.Run("db error propagates", func(t *testing.T) {
		mock.ExpectQuery(`SELECT \* FROM "payment_transactions" WHERE reference_number = \$1`).
			WillReturnError(errors.New("connection reset"))

		_, err := repo.GetByReferenceNumber(context.Background(), "TNSW1")
		require.Error(t, err)
	})

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRepository_GetByReferenceNumberForUpdate(t *testing.T) {
	db, mock := setupTestDB(t)
	repo := NewPaymentRepository(db)

	t.Run("emits FOR UPDATE lock", func(t *testing.T) {
		rows := sqlmock.NewRows([]string{"id", "reference_number", "status"}).
			AddRow("uuid-1", "TNSW1", PaymentStatusPending)
		// The query must carry the row-level write lock so concurrent webhooks serialize.
		mock.ExpectQuery(`SELECT \* FROM "payment_transactions" WHERE reference_number = \$1.*FOR UPDATE`).
			WillReturnRows(rows)

		res, err := repo.GetByReferenceNumberForUpdate(context.Background(), "TNSW1")
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.Equal(t, "TNSW1", res.ReferenceNumber)
	})

	t.Run("not found returns nil,nil", func(t *testing.T) {
		mock.ExpectQuery(`SELECT \* FROM "payment_transactions" WHERE reference_number = \$1.*FOR UPDATE`).
			WillReturnError(gorm.ErrRecordNotFound)

		res, err := repo.GetByReferenceNumberForUpdate(context.Background(), "UNKNOWN")
		require.NoError(t, err)
		assert.Nil(t, res)
	})

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRepository_GetByTaskID(t *testing.T) {
	db, mock := setupTestDB(t)
	repo := NewPaymentRepository(db)

	t.Run("found", func(t *testing.T) {
		rows := sqlmock.NewRows([]string{"id", "task_id", "status"}).
			AddRow("uuid-1", "task-1", PaymentStatusPending)
		mock.ExpectQuery(`SELECT \* FROM "payment_transactions" WHERE task_id = \$1`).
			WillReturnRows(rows)

		res, err := repo.GetByTaskID(context.Background(), "task-1")
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.Equal(t, "task-1", res.TaskID)
	})

	t.Run("not found returns nil,nil", func(t *testing.T) {
		mock.ExpectQuery(`SELECT \* FROM "payment_transactions" WHERE task_id = \$1`).
			WillReturnError(gorm.ErrRecordNotFound)

		res, err := repo.GetByTaskID(context.Background(), "UNKNOWN")
		require.NoError(t, err)
		assert.Nil(t, res)
	})

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRepository_Update(t *testing.T) {
	db, mock := setupTestDB(t)
	repo := NewPaymentRepository(db)

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "payment_transactions" SET`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := repo.Update(context.Background(), &PaymentTransaction{
		ID:              "uuid-1",
		ReferenceNumber: "TNSW1",
		Status:          PaymentStatusSuccess,
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRepository_UpdateStatus(t *testing.T) {
	db, mock := setupTestDB(t)
	repo := NewPaymentRepository(db)

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "payment_transactions" SET "status"=\$1,"updated_at"=\$2 WHERE reference_number = \$3`).
		WithArgs(PaymentStatusSuccess, sqlmock.AnyArg(), "TNSW1").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.UpdateStatus(context.Background(), "TNSW1", PaymentStatusSuccess))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRepository_RunInTransaction_Commit(t *testing.T) {
	db, mock := setupTestDB(t)
	repo := NewPaymentRepository(db)

	// The fn runs an Update through the tx-bound repo; expect it between BEGIN/COMMIT.
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "payment_transactions" SET`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := repo.RunInTransaction(context.Background(), func(r PaymentRepository) error {
		return r.Update(context.Background(), &PaymentTransaction{ID: "uuid-1", ReferenceNumber: "TNSW1", Status: PaymentStatusSuccess})
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRepository_RunInTransaction_Rollback(t *testing.T) {
	db, mock := setupTestDB(t)
	repo := NewPaymentRepository(db)

	mock.ExpectBegin()
	mock.ExpectRollback()

	wantErr := errors.New("boom")
	err := repo.RunInTransaction(context.Background(), func(r PaymentRepository) error {
		return wantErr
	})
	require.ErrorIs(t, err, wantErr)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRepository_WithTx(t *testing.T) {
	db, _ := setupTestDB(t)
	repo := NewPaymentRepository(db)
	assert.NotNil(t, repo.WithTx(db))
}

func TestRepository_GetByReferenceNumberForUpdate_DBError(t *testing.T) {
	db, mock := setupTestDB(t)
	repo := NewPaymentRepository(db)

	mock.ExpectQuery(`SELECT \* FROM "payment_transactions" WHERE reference_number = \$1.*FOR UPDATE`).
		WillReturnError(errors.New("connection reset"))

	_, err := repo.GetByReferenceNumberForUpdate(context.Background(), "TNSW1")
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRepository_GetByTaskID_DBError(t *testing.T) {
	db, mock := setupTestDB(t)
	repo := NewPaymentRepository(db)

	mock.ExpectQuery(`SELECT \* FROM "payment_transactions" WHERE task_id = \$1`).
		WillReturnError(errors.New("connection reset"))

	_, err := repo.GetByTaskID(context.Background(), "task-1")
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
