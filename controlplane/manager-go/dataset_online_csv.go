package main

import (
	"encoding/csv"
	"io"
	"strings"
)

const datasetTableByteLimit = 4 * 1024 * 1024
const datasetPreviewByteLimit = 1024 * 1024

// csv.Reader normalizes CRLF even inside quoted fields. Temporarily replacing
// quoted CR preserves the original cell text while retaining its CSV parser.
// Uploaded CSV rejects NUL; reject it here as well to keep the marker unambiguous.
type datasetQuotedCRReader struct {
	input  io.Reader
	quoted bool
}

func (r *datasetQuotedCRReader) Read(data []byte) (int, error) {
	n, e := r.input.Read(data)
	for i, b := range data[:n] {
		switch b {
		case 0:
			return i, api(400, "invalid_csv", "CSV须为不含NUL的UTF-8文本")
		case '"':
			// Doubled quotes toggle twice, including when split across reads.
			r.quoted = !r.quoted
		case '\r':
			if r.quoted {
				data[i] = 0
			}
		}
	}
	return n, e
}

// This budget resets for each CSV record. csv.Reader may retain up to its 4 KiB
// buffered read-ahead between records, independently of the bounded page output.
type datasetRecordBudgetReader struct {
	input     io.Reader
	remaining int64
	limit     int64
	tooLarge  error
}

func (r *datasetRecordBudgetReader) Read(data []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, r.tooLarge
	}
	if int64(len(data)) > r.remaining {
		data = data[:int(r.remaining)]
	}
	n, e := r.input.Read(data)
	r.remaining -= int64(n)
	return n, e
}

type datasetOnlineCSVReader struct {
	*csv.Reader
	budget *datasetRecordBudgetReader
}

func newDatasetOnlineCSVReader(input io.Reader, delimiter rune, recordLimit int64, tooLarge error) *datasetOnlineCSVReader {
	var source io.Reader = &datasetQuotedCRReader{input: input}
	var budget *datasetRecordBudgetReader
	if recordLimit > 0 {
		budget = &datasetRecordBudgetReader{input: source, limit: recordLimit, remaining: recordLimit, tooLarge: tooLarge}
		source = budget
	}
	return &datasetOnlineCSVReader{Reader: datasetCSVReader(source, delimiter), budget: budget}
}

func (r *datasetOnlineCSVReader) Read() ([]string, error) {
	if r.budget != nil {
		r.budget.remaining = r.budget.limit
	}
	record, e := r.Reader.Read()
	if e != nil {
		return nil, e
	}
	for i, value := range record {
		record[i] = strings.ReplaceAll(value, "\x00", "\r")
	}
	return record, nil
}

func datasetOnlineRowBytes(row []string, priorRows int) int {
	// Rows are [][]string in both responses, serialized by encoding/json.
	size := datasetOnlineJSONBytes(row)
	if priorRows > 0 {
		size++ // JSON array separator; the empty array is already in the envelope.
	}
	return size
}

func datasetOnlineJSONBytes(value any) int {
	data := encoded(value)
	size := len(data)
	// encoding/json escapes HTML and Unicode line separators for nested rows
	// and for GET's gzip envelope. Account for either response encoding.
	for i := 0; i < len(data); i++ {
		switch data[i] {
		case '<', '>', '&':
			size += 5
		case 0xe2:
			if i+2 < len(data) && data[i+1] == 0x80 && (data[i+2] == 0xa8 || data[i+2] == 0xa9) {
				size += 3
				i += 2
			}
		}
	}
	return size
}
