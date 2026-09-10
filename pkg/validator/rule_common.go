package valx

import (
	"reflect"
	"strings"

	"github.com/asaskevich/govalidator"
	v "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/go-ozzo/ozzo-validation/v4/is"
	"github.com/samber/lo"
)

type (
	// FieldRules is the set of rules bound to a single struct field.
	FieldRules = v.FieldRules
	// Rule is a single validation rule.
	Rule = v.Rule
)

// Commonly used validation rules
var (
	Field                  = v.Field
	Required               = v.Required
	NotNil                 = v.NotNil
	NilOrNotEmpty          = v.NilOrNotEmpty
	Date                   = v.Date
	In                     = v.In
	When                   = v.When
	Length                 = v.Length
	RuneLength             = v.RuneLength
	Map                    = v.Map
	Key                    = v.Key
	Each                   = v.Each
	Match                  = v.Match
	NotIn                  = v.NotIn
	Max                    = v.Max
	Min                    = v.Min
	MultipleOf             = v.MultipleOf
	NewStringRule          = v.NewStringRule
	NewStringRuleWithError = v.NewStringRuleWithError
	By                     = v.By
)

var (
	// Email validates if a string is an email or not. It also checks if the MX record exists for the email domain.
	Email = is.EmailFormat
	// EmailFormat validates if a string is an email or not. Note that it does NOT check if the MX record exists or not.
	EmailFormat = is.EmailFormat
	// URL validates if a string is a valid URL
	URL = is.URL
	// RequestURL validates if a string is a valid request URL
	RequestURL = is.RequestURL
	// RequestURI validates if a string is a valid request URI
	RequestURI = is.RequestURI
	// Alpha validates if a string contains English letters only (a-zA-Z)
	Alpha = is.Alpha
	// Digit validates if a string contains digits only (0-9)
	Digit = is.Digit
	// Alphanumeric validates if a string contains English letters and digits only (a-zA-Z0-9)
	Alphanumeric = is.Alphanumeric
	// UTFLetter validates if a string contains unicode letters only
	UTFLetter = is.UTFLetter
	// UTFDigit validates if a string contains unicode decimal digits only
	UTFDigit = is.UTFDigit
	// UTFLetterNumeric validates if a string contains unicode letters and numbers only
	UTFLetterNumeric = is.UTFLetterNumeric
	// UTFNumeric validates if a string contains unicode number characters (category N) only
	UTFNumeric = is.UTFNumeric
	// LowerCase validates if a string contains lower case unicode letters only
	LowerCase = is.LowerCase
	// UpperCase validates if a string contains upper case unicode letters only
	UpperCase = is.UpperCase
	// Hexadecimal validates if a string is a valid hexadecimal number
	Hexadecimal = is.Hexadecimal
	// HexColor validates if a string is a valid hexadecimal color code
	HexColor = is.HexColor
	// RGBColor validates if a string is a valid RGB color in the form of rgb(R, G, B)
	RGBColor = is.RGBColor
	// Int validates if a string is a valid integer number
	Int = is.Int
	// Float validates if a string is a floating point number
	Float = is.Float
	// UUIDv3 validates if a string is a valid version 3 UUID
	UUIDv3 = is.UUIDv3
	// UUIDv4 validates if a string is a valid version 4 UUID
	UUIDv4 = is.UUIDv4
	// UUIDv5 validates if a string is a valid version 5 UUID
	UUIDv5 = is.UUIDv5
	// UUID validates if a string is a valid UUID
	UUID = is.UUID
	// CreditCard validates if a string is a valid credit card number
	CreditCard = is.CreditCard
	// ISBN10 validates if a string is an ISBN version 10
	ISBN10 = is.ISBN10
	// ISBN13 validates if a string is an ISBN version 13
	ISBN13 = is.ISBN13
	// ISBN validates if a string is an ISBN (either version 10 or 13)
	ISBN = is.ISBN
	// JSON validates if a string is in valid JSON format
	JSON = is.JSON
	// ASCII validates if a string contains ASCII characters only
	ASCII = is.ASCII
	// PrintableASCII validates if a string contains printable ASCII characters only
	PrintableASCII = is.PrintableASCII
	// Multibyte validates if a string contains multibyte characters
	Multibyte = is.Multibyte
	// FullWidth validates if a string contains full-width characters
	FullWidth = is.FullWidth
	// HalfWidth validates if a string contains half-width characters
	HalfWidth = is.HalfWidth
	// VariableWidth validates if a string contains both full-width and half-width characters
	VariableWidth = is.VariableWidth
	// Base64 validates if a string is encoded in Base64
	Base64 = is.Base64
	// DataURI validates if a string is a valid base64-encoded data URI
	DataURI = is.DataURI
	// E164 validates if a string is a valid E.164 phone number
	E164 = is.E164
	// CountryCode2 validates if a string is a valid ISO3166 Alpha 2 country code
	CountryCode2 = is.CountryCode2
	// CountryCode3 validates if a string is a valid ISO3166 Alpha 3 country code
	CountryCode3 = is.CountryCode3
	// CurrencyCode validates if a string is a valid IsISO4217 currency code.
	CurrencyCode = v.By(func(value any) error {
		r := reflect.ValueOf(value)

		str := r.String()

		_, exist := lo.Find(govalidator.ISO4217List, func(code string) bool {
			return strings.EqualFold(code, str)
		})

		if !exist {
			return is.ErrCurrencyCode
		}

		return nil
	})
	// DialString validates if a string is a valid dial string that can be passed to Dial()
	DialString = is.DialString
	// MAC validates if a string is a MAC address
	MAC = is.MAC
	// IP validates if a string is a valid IP address (either version 4 or 6)
	IP = is.IP
	// IPv4 validates if a string is a valid version 4 IP address
	IPv4 = is.IPv4
	// IPv6 validates if a string is a valid version 6 IP address
	IPv6 = is.IPv6
	// Subdomain validates if a string is valid subdomain
	Subdomain = is.Subdomain
	// Domain validates if a string is valid domain
	Domain = is.Domain
	// DNSName validates if a string is valid DNS name
	DNSName = is.DNSName
	// Host validates if a string is a valid IP (both v4 and v6) or a valid DNS name
	Host = is.Host
	// Port validates if a string is a valid port number
	Port = is.Port
	// MongoID validates if a string is a valid Mongo ID
	MongoID = is.MongoID
	// Latitude validates if a string is a valid latitude
	Latitude = is.Latitude
	// Longitude validates if a string is a valid longitude
	Longitude = is.Longitude
	// SSN validates if a string is a social security number (SSN)
	SSN = is.SSN
	// Semver validates if a string is a valid semantic version
	Semver = is.Semver
)

// WhenNotInitialize returns a factory that applies the given rules except
// while the struct is being initialized. Passing true suppresses them;
// passing false, or no argument at all, applies them. Arguments after the
// first are ignored.
//
// The two stages let one decision gate several fields:
//
//	gate := valx.WhenNotInitialize(isCreate)
//	valx.Struct(&s,
//		valx.Field(&s.Name, gate(valx.Required)),
//		valx.Field(&s.Slug, gate(valx.Required)),
//	)
func WhenNotInitialize(initial ...bool) func(rules ...Rule) Rule {
	return func(rules ...Rule) Rule {
		nocheck := len(initial) > 0 && initial[0]

		return When(!nocheck, rules...)
	}
}
