// Package dto defines the identity API's public projection.
package dto

// Me exposes the stable profile and current Auth email without tokens or the Auth UUID.
type Me struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
}
