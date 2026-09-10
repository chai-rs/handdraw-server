package bunx

import "database/sql"

// NullString converts a string to its nullable database representation.
func NullString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

// NullStringPtr converts nil to SQL NULL and a non-nil pointer to a valid string.
func NullStringPtr(value *string) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}

	return sql.NullString{String: *value, Valid: true}
}

// StringValue returns an empty string for SQL NULL.
func StringValue(value sql.NullString) string {
	if !value.Valid {
		return ""
	}

	return value.String
}

// StringPtr converts SQL NULL to nil and a valid string to a pointer.
func StringPtr(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}

	result := value.String

	return &result
}
