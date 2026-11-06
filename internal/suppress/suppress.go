// Package suppress hides the operating system's own notification banners while a focus level is active.
package suppress

// Suppressor hides the OS banners shh has already seen. Start and Stop are idempotent: starting a running suppressor or stopping
// a stopped one is not an error.
type Suppressor interface {
	Start() error
	Stop() error
}
