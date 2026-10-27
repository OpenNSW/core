// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package payment

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Repository defines the interface for managing payment Transactions.
type Repository interface {
	// Create stores a new transaction. A reference another transaction holds
	// returns ErrDuplicateReference.
	Create(ctx context.Context, tx *Transaction) error
	GetByReferenceNumber(ctx context.Context, referenceNumber string) (*Transaction, error)
	// GetByReferenceNumberForUpdate reads a transaction while holding a row-level
	// write lock (SELECT ... FOR UPDATE). Must be called inside RunInTransaction.
	GetByReferenceNumberForUpdate(ctx context.Context, referenceNumber string) (*Transaction, error)
	Update(ctx context.Context, tx *Transaction) error
	UpdateStatus(ctx context.Context, referenceNumber string, status Status) error
	// RunInTransaction runs fn inside a DB transaction, passing a repository bound
	// to that transaction. The transaction commits when fn returns nil and rolls
	// back on error.
	RunInTransaction(ctx context.Context, fn func(repo Repository) error) error
	WithTx(tx *gorm.DB) Repository
}

type paymentRepository struct {
	db *gorm.DB
}

// NewRepository creates a new instance of Repository.
func NewRepository(db *gorm.DB) Repository {
	return &paymentRepository{db: db}
}

// WithTx enables transaction propagation.
func (r *paymentRepository) WithTx(tx *gorm.DB) Repository {
	return NewRepository(tx)
}

// RunInTransaction runs fn within a single DB transaction.
func (r *paymentRepository) RunInTransaction(ctx context.Context, fn func(repo Repository) error) error {
	return r.db.WithContext(ctx).Transaction(func(txDB *gorm.DB) error {
		return fn(r.WithTx(txDB))
	})
}

// GetByReferenceNumberForUpdate retrieves a transaction while holding a row-level
// write lock so concurrent webhook deliveries serialize on the same record.
func (r *paymentRepository) GetByReferenceNumberForUpdate(ctx context.Context, referenceNumber string) (*Transaction, error) {
	var ptx Transaction
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

// Create inserts a new Transaction into the database.
func (r *paymentRepository) Create(ctx context.Context, ptx *Transaction) error {
	err := r.db.WithContext(ctx).Create(ptx).Error
	if isDuplicateKey(r.db, err) && r.referenceHeldByAnother(ctx, ptx) {
		return fmt.Errorf("%w: %s", ErrDuplicateReference, ptx.ReferenceNumber)
	}
	return err
}

// referenceHeldByAnother reports whether a transaction other than ptx holds
// ptx's reference. It tells a duplicate reference apart from the other unique
// violations an insert can hit, such as an existing ID, which drivers report
// with the same error code.
func (r *paymentRepository) referenceHeldByAnother(ctx context.Context, ptx *Transaction) bool {
	holder, err := r.GetByReferenceNumber(ctx, ptx.ReferenceNumber)
	return err == nil && holder != nil && holder.ID != ptx.ID
}

// isDuplicateKey reports whether err is a unique-constraint violation, as
// gorm.ErrDuplicatedKey or as the dialector translates the driver's error.
func isDuplicateKey(db *gorm.DB, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	if translator, ok := db.Dialector.(gorm.ErrorTranslator); ok {
		return errors.Is(translator.Translate(err), gorm.ErrDuplicatedKey)
	}
	return false
}

// GetByReferenceNumber retrieves a Transaction by its reference number.
func (r *paymentRepository) GetByReferenceNumber(ctx context.Context, referenceNumber string) (*Transaction, error) {
	var ptx Transaction
	if err := r.db.WithContext(ctx).Where("reference_number = ?", referenceNumber).First(&ptx).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil // Return nil, nil when not found to easily check existence
		}
		return nil, err
	}
	return &ptx, nil
}

// Update saves changes to an existing Transaction.
func (r *paymentRepository) Update(ctx context.Context, ptx *Transaction) error {
	return r.db.WithContext(ctx).Save(ptx).Error
}

// UpdateStatus updates only the status field of a Transaction.
func (r *paymentRepository) UpdateStatus(ctx context.Context, referenceNumber string, status Status) error {
	return r.db.WithContext(ctx).Model(&Transaction{}).Where("reference_number = ?", referenceNumber).Updates(map[string]interface{}{"status": status}).Error
}
