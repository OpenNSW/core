package payment

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PaymentRepository defines the interface for managing PaymentTransactions.
type PaymentRepository interface {
	Create(ctx context.Context, tx *PaymentTransaction) error
	GetByReferenceNumber(ctx context.Context, referenceNumber string) (*PaymentTransaction, error)
	// GetByReferenceNumberForUpdate reads a transaction while holding a row-level
	// write lock (SELECT ... FOR UPDATE). Must be called inside RunInTransaction.
	GetByReferenceNumberForUpdate(ctx context.Context, referenceNumber string) (*PaymentTransaction, error)
	GetByTaskID(ctx context.Context, taskID string) (*PaymentTransaction, error)
	Update(ctx context.Context, tx *PaymentTransaction) error
	UpdateStatus(ctx context.Context, referenceNumber string, status PaymentStatus) error
	// RunInTransaction runs fn inside a DB transaction, passing a repository bound
	// to that transaction. The transaction commits when fn returns nil and rolls
	// back on error.
	RunInTransaction(ctx context.Context, fn func(repo PaymentRepository) error) error
	WithTx(tx *gorm.DB) PaymentRepository
}

type paymentRepository struct {
	db *gorm.DB
}

// NewPaymentRepository creates a new instance of PaymentRepository.
func NewPaymentRepository(db *gorm.DB) PaymentRepository {
	return &paymentRepository{db: db}
}

// WithTx enables transaction propagation.
func (r *paymentRepository) WithTx(tx *gorm.DB) PaymentRepository {
	return NewPaymentRepository(tx)
}

// RunInTransaction runs fn within a single DB transaction.
func (r *paymentRepository) RunInTransaction(ctx context.Context, fn func(repo PaymentRepository) error) error {
	return r.db.WithContext(ctx).Transaction(func(txDB *gorm.DB) error {
		return fn(r.WithTx(txDB))
	})
}

// GetByReferenceNumberForUpdate retrieves a transaction while holding a row-level
// write lock so concurrent webhook deliveries serialize on the same record.
func (r *paymentRepository) GetByReferenceNumberForUpdate(ctx context.Context, referenceNumber string) (*PaymentTransaction, error) {
	var ptx PaymentTransaction
	err := r.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("reference_number = ?", referenceNumber).
		First(&ptx).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &ptx, nil
}

// Create inserts a new PaymentTransaction into the database.
func (r *paymentRepository) Create(ctx context.Context, ptx *PaymentTransaction) error {
	return r.db.WithContext(ctx).Create(ptx).Error
}

// GetByReferenceNumber retrieves a PaymentTransaction by its reference number.
func (r *paymentRepository) GetByReferenceNumber(ctx context.Context, referenceNumber string) (*PaymentTransaction, error) {
	var ptx PaymentTransaction
	if err := r.db.WithContext(ctx).Where("reference_number = ?", referenceNumber).First(&ptx).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil // Return nil, nil when not found to easily check existence
		}
		return nil, err
	}
	return &ptx, nil
}

// GetByTaskID retrieves a PaymentTransaction by its associated TaskID.
func (r *paymentRepository) GetByTaskID(ctx context.Context, taskID string) (*PaymentTransaction, error) {
	var ptx PaymentTransaction
	if err := r.db.WithContext(ctx).Where("task_id = ?", taskID).First(&ptx).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &ptx, nil
}

// Update saves changes to an existing PaymentTransaction.
func (r *paymentRepository) Update(ctx context.Context, ptx *PaymentTransaction) error {
	return r.db.WithContext(ctx).Save(ptx).Error
}

// UpdateStatus updates only the status field of a PaymentTransaction.
func (r *paymentRepository) UpdateStatus(ctx context.Context, referenceNumber string, status PaymentStatus) error {
	return r.db.WithContext(ctx).Model(&PaymentTransaction{}).Where("reference_number = ?", referenceNumber).Updates(map[string]interface{}{"status": status}).Error
}
