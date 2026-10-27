package importer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testFileMode = 0o600

func writeCSV(t *testing.T, dir, name, content string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), testFileMode); err != nil {
		t.Fatal(err)
	}
}

func TestOpenCSVAcceptsExactHeaderAndBOM(t *testing.T) {
	dir := t.TempDir()
	writeCSV(t, dir, fileMeasureUnit, utf8BOM+"\"id\",\"name\"\n\"1000\",\"cup\"\n")

	file, err := openCSV(dir, fileMeasureUnit)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = file.Close() }()

	record, err := file.Next()
	if err != nil {
		t.Fatal(err)
	}

	if record[0] != "1000" || record[1] != "cup" {
		t.Fatalf("got %q", record)
	}
}

func TestOpenCSVRejectsRenamedColumn(t *testing.T) {
	dir := t.TempDir()
	writeCSV(t, dir, fileMeasureUnit, "\"id\",\"unit_name\"\n")

	_, err := openCSV(dir, fileMeasureUnit)
	if err == nil || !strings.Contains(err.Error(), `column 2 is "unit_name", want "name"`) {
		t.Fatalf("got %v", err)
	}
}

func TestOpenCSVRejectsExtraColumn(t *testing.T) {
	dir := t.TempDir()
	writeCSV(t, dir, fileMeasureUnit, "\"id\",\"name\",\"extra\"\n")

	_, err := openCSV(dir, fileMeasureUnit)
	if err == nil || !strings.Contains(err.Error(), "header has 3 columns, want 2") {
		t.Fatalf("got %v", err)
	}
}

func TestOpenCSVRejectsUnknownFile(t *testing.T) {
	if _, err := openCSV(t.TempDir(), "mystery.csv"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestNextReportsRowNumbers(t *testing.T) {
	dir := t.TempDir()
	writeCSV(t, dir, fileMeasureUnit, "\"id\",\"name\"\n\"1\",\"ok\"\n\"2\",\"too\",\"many\"\n")

	file, err := openCSV(dir, fileMeasureUnit)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = file.Close() }()

	if _, err := file.Next(); err != nil {
		t.Fatal(err)
	}

	_, err = file.Next()
	if err == nil || !strings.Contains(err.Error(), "measure_unit.csv row 3") {
		t.Fatalf("got %v", err)
	}
}

func TestText(t *testing.T) {
	if value := text(""); value != nil {
		t.Fatalf("text(\"\") = %v, want nil", value)
	}

	if value := text("a"); value != "a" {
		t.Fatalf("text(a) = %v", value)
	}
}

func TestNullFloat(t *testing.T) {
	file := &csvFile{name: "x.csv", row: 7}

	cases := map[string]struct {
		input string
		valid bool
		want  float64
		fails bool
	}{
		"empty":   {input: ""},
		"decimal": {input: "2.71", valid: true, want: 2.71},
		"word":    {input: "abc", fails: true},
	}
	for name, tc := range cases {
		value, err := file.nullFloat(tc.input, colAmount)
		if (err != nil) != tc.fails {
			t.Fatalf("%s: err = %v", name, err)
		}

		if value.Valid != tc.valid || value.Float64 != tc.want {
			t.Fatalf("%s: got %+v", name, value)
		}
	}

	_, err := file.nullFloat("abc", colAmount)
	if err == nil || !strings.Contains(err.Error(), "x.csv row 7: amount") {
		t.Fatalf("error must name the file, row and column: %v", err)
	}
}

func TestNullInt(t *testing.T) {
	file := &csvFile{name: "x.csv", row: 7}

	cases := map[string]struct {
		input string
		valid bool
		want  int64
		fails bool
	}{
		"empty":     {input: ""},
		"integer":   {input: "10", valid: true, want: 10},
		"whole":     {input: "1.0", valid: true, want: 1},
		"fraction":  {input: "1.5", fails: true},
		"not a num": {input: "x", fails: true},
	}
	for name, tc := range cases {
		value, err := file.nullInt(tc.input, colDataPoints)
		if (err != nil) != tc.fails {
			t.Fatalf("%s: err = %v", name, err)
		}

		if value.Valid != tc.valid || value.Int64 != tc.want {
			t.Fatalf("%s: got %+v", name, value)
		}
	}
}

func TestMustInt(t *testing.T) {
	file := &csvFile{name: "x.csv", row: 7}

	if value, err := file.mustInt("42", colFDCID); err != nil || value != 42 {
		t.Fatalf("mustInt 42 = %d, %v", value, err)
	}

	if _, err := file.mustInt("", colFDCID); err == nil {
		t.Fatal("mustInt empty must fail")
	}
}
