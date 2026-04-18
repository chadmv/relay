//go:build integration

package api

import "golang.org/x/crypto/bcrypt"

// SetBcryptCostForTest sets bcrypt cost to the minimum so integration tests run fast.
func SetBcryptCostForTest() { bcryptCost = bcrypt.MinCost }
