package importer

import (
	"context"
	"database/sql"
	"fmt"
)

// batchWriter runs one prepared INSERT inside transactions of size rows.
// Committing every row would take hours on 27 million nutrient rows.
type batchWriter struct {
	database *sql.DB
	query    string
	size     int
	tx       *sql.Tx
	stmt     *sql.Stmt
	inTx     int
	total    int64
}

func newBatchWriter(database *sql.DB, query string, size int) *batchWriter {
	return &batchWriter{database: database, query: query, size: size}
}

func (writer *batchWriter) begin(ctx context.Context) error {
	tx, err := writer.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin batch: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, writer.query)
	if err != nil {
		_ = tx.Rollback()

		return fmt.Errorf("prepare batch: %w", err)
	}

	writer.tx, writer.stmt, writer.inTx = tx, stmt, 0

	return nil
}

func (writer *batchWriter) commit() error {
	if writer.tx == nil {
		return nil
	}

	_ = writer.stmt.Close()

	err := writer.tx.Commit()
	writer.tx, writer.stmt = nil, nil

	if err != nil {
		return fmt.Errorf("commit batch: %w", err)
	}

	return nil
}

// Write inserts one row, opening and committing transactions as needed.
func (writer *batchWriter) Write(ctx context.Context, args ...any) error {
	if writer.tx == nil {
		if err := writer.begin(ctx); err != nil {
			return err
		}
	}

	if _, err := writer.stmt.ExecContext(ctx, args...); err != nil {
		return fmt.Errorf("insert row: %w", err)
	}

	writer.inTx++
	writer.total++

	if writer.inTx >= writer.size {
		return writer.commit()
	}

	return nil
}

// Close commits whatever is pending.
func (writer *batchWriter) Close() error {
	return writer.commit()
}

// Abort rolls back a pending transaction after a failure.
func (writer *batchWriter) Abort() {
	if writer.tx != nil {
		_ = writer.stmt.Close()
		_ = writer.tx.Rollback()
		writer.tx, writer.stmt = nil, nil
	}
}
