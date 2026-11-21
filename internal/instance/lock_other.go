//go:build !windows && !unix

package instance

func lock(string) (func(), error) { return func() {}, nil }
