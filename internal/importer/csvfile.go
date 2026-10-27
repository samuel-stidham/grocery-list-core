package importer

import (
	"bufio"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

// utf8BOM is the byte order mark some exporters put before the first header.
const utf8BOM = "\xef\xbb\xbf"

// readBufferSize is the read-ahead for the large CSV files, 1 MiB.
const readBufferSize = 1 << 20

// csvFile is one USDA file open for streaming. Every read error carries the
// file name and the row number.
type csvFile struct {
	name   string
	handle *os.File
	reader *csv.Reader
	row    int
}

// openCSV opens dir/name and checks its header against expectedHeaders.
func openCSV(dir, name string) (*csvFile, error) {
	want, known := expectedHeaders()[name]
	if !known {
		return nil, fmt.Errorf("%w: %s", ErrUnknownFile, name)
	}

	handle, err := os.Open(filepath.Clean(filepath.Join(dir, name)))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrMissingFile, name)
		}

		return nil, fmt.Errorf("open %s: %w", name, err)
	}

	// The CSV parser treats a byte order mark as data and then chokes on the
	// quote that follows it, so the mark is skipped at the byte level.
	buffered := bufio.NewReaderSize(handle, readBufferSize)
	if lead, err := buffered.Peek(len(utf8BOM)); err == nil && string(lead) == utf8BOM {
		_, _ = buffered.Discard(len(utf8BOM))
	}

	reader := csv.NewReader(buffered)
	reader.ReuseRecord = true

	header, err := reader.Read()
	if err != nil {
		_ = handle.Close()

		return nil, fmt.Errorf("%s: read header: %w", name, err)
	}

	if err := checkHeader(name, header, want); err != nil {
		_ = handle.Close()

		return nil, err
	}

	reader.FieldsPerRecord = len(want)

	return &csvFile{name: name, handle: handle, reader: reader, row: 1}, nil
}

func checkHeader(name string, got, want []string) error {
	if len(got) != len(want) {
		return fmt.Errorf("%w: %s: header has %d columns, want %d: got %q", ErrHeaderMismatch, name, len(got), len(want), got)
	}

	for index := range want {
		if got[index] != want[index] {
			return fmt.Errorf("%w: %s: header column %d is %q, want %q",
				ErrHeaderMismatch, name, index+1, got[index], want[index])
		}
	}

	return nil
}

// Next returns the next record. The slice is reused between calls. io.EOF
// marks the end.
func (file *csvFile) Next() ([]string, error) {
	record, err := file.reader.Read()
	if errors.Is(err, io.EOF) {
		return nil, io.EOF
	}

	file.row++

	if err != nil {
		return nil, fmt.Errorf("%s row %d: %w", file.name, file.row, err)
	}

	return record, nil
}

// Close releases the file handle.
func (file *csvFile) Close() error {
	if err := file.handle.Close(); err != nil {
		return fmt.Errorf("close %s: %w", file.name, err)
	}

	return nil
}

// errorf builds an error that names the file and the current row.
func (file *csvFile) errorf(format string, args ...any) error {
	return fmt.Errorf("%w: %s row %d: %s", ErrBadValue, file.name, file.row, fmt.Sprintf(format, args...))
}

// text returns nil for the empty string, which USDA uses for null.
func text(value string) any {
	if value == "" {
		return nil
	}

	return value
}

// nullFloat parses a decimal. The empty string is null.
func (file *csvFile) nullFloat(value, column string) (sql.NullFloat64, error) {
	if value == "" {
		return sql.NullFloat64{}, nil
	}

	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return sql.NullFloat64{}, file.errorf("%s: %q is not a number", column, value)
	}

	return sql.NullFloat64{Float64: parsed, Valid: true}, nil
}

// nullInt parses a whole number. The empty string is null. USDA writes some
// integer columns as "1.0", so a decimal with no fraction is accepted.
func (file *csvFile) nullInt(value, column string) (sql.NullInt64, error) {
	if value == "" {
		return sql.NullInt64{}, nil
	}

	if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
		return sql.NullInt64{Int64: parsed, Valid: true}, nil
	}

	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || parsed != float64(int64(parsed)) {
		return sql.NullInt64{}, file.errorf("%s: %q is not a whole number", column, value)
	}

	return sql.NullInt64{Int64: int64(parsed), Valid: true}, nil
}

// mustInt parses a required integer such as a primary key.
func (file *csvFile) mustInt(value, column string) (int64, error) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, file.errorf("%s: %q is not an integer", column, value)
	}

	return parsed, nil
}
