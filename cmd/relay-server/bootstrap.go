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

	if _, err := q.CreateUser(ctx, store.CreateUserParams{
		Name:    email,
		Email:   email,
		IsAdmin: true,
	}); err != nil {
		return fmt.Errorf("create admin user: %w", err)
	}
	log.Printf("bootstrap admin ready (created new user): %s", email)
	return nil
}
