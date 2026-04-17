package main

import (
	"context"
	"fmt"
	"log"

	"relay/internal/store"
)

// bootstrapAdmin creates or promotes an admin user when no admin exists.
// Safe to call on every startup — becomes a no-op once any admin exists.
func bootstrapAdmin(ctx context.Context, q *store.Queries, email string) error {
	exists, err := q.AdminExists(ctx)
	if err != nil {
		return fmt.Errorf("check admin exists: %w", err)
	}
	if exists {
		log.Println("bootstrap-admin skipped: admin already exists")
		return nil
	}

	user, err := q.GetUserByEmail(ctx, email)
	if err == nil {
		if err := q.PromoteUserToAdmin(ctx, user.ID); err != nil {
			return fmt.Errorf("promote user to admin: %w", err)
		}
		log.Printf("bootstrap admin ready (promoted existing user): %s", email)
		return nil
	}

	newUser, err := q.CreateUser(ctx, store.CreateUserParams{
		Name:    email,
		Email:   email,
		IsAdmin: true,
	})
	if err != nil {
		// Another instance may have created the user concurrently. Retry lookup.
		existingUser, lookupErr := q.GetUserByEmail(ctx, email)
		if lookupErr != nil {
			return fmt.Errorf("create admin user: %w", err)
		}
		newUser = existingUser
		if err := q.PromoteUserToAdmin(ctx, newUser.ID); err != nil {
			return fmt.Errorf("promote user to admin (concurrent startup): %w", err)
		}
		log.Printf("bootstrap admin ready (concurrent startup, promoted): %s", email)
		return nil
	}
	_ = newUser
	log.Printf("bootstrap admin ready (created new user): %s", email)
	return nil
}
