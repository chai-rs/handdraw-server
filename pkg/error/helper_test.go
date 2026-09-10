package errx

func withoutStacktrace(o Error) Error {
	o.stacktrace = nil
	return o
}
