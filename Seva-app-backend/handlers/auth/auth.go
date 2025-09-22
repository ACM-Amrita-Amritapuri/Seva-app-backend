package auth

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	mw "Seva-app-backend/middleware"
	"Seva-app-backend/models"
)

func Register(g fiber.Router, pool *pgxpool.Pool, jwtGuard fiber.Handler, requireAdmin fiber.Handler) {
	// Public routes
	g.Post("/login", login(pool))                          // Generic login (faculty/admin or volunteer)
	g.Post("/register/volunteer", registerVolunteer(pool)) // Student self-registration (UPDATED)
	g.Post("/refresh", refresh(pool))                      // For Faculty/Admin refresh tokens

	// Protected routes
	g.Get("/me", jwtGuard, me())
	g.Post("/logout", jwtGuard, logout(pool))

	// Admin-only routes
	g.Post("/register/faculty", jwtGuard, requireAdmin, registerFaculty(pool)) // Admin registers faculty/admin
}

// ---------- Helper Functions (moved here for reuse) ----------
// BcryptHash hashes a plain text password.
func BcryptHash(plain string) (string, error) {
	if plain == "" {
		return "", errors.New("password cannot be empty")
	}
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("failed to hash password: %w", err)
	}
	return string(b), nil
}

// BcryptVerify compares a hashed password with a plain text password.
func BcryptVerify(hash, plain string) bool {
	if hash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// sha256b64 hashes a string with SHA256 and base64-encodes it.
func sha256b64(s string) string {
	h := sha256.Sum256([]byte(s))
	return base64.StdEncoding.EncodeToString(h[:])
}

// ttlFromEnv parses a duration from an environment variable, or returns a default.
func ttlFromEnv(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

// ---------- /auth/login (Generic Login) ----------
func login(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var b models.LoginRequest
		if err := c.BodyParser(&b); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Bad JSON")
		}
		email := strings.ToLower(strings.TrimSpace(b.Email))
		if email == "" || b.Password == "" {
			return fiber.NewError(fiber.StatusBadRequest, "Email and password required")
		}

		var userID int64
		var hash sql.NullString
		var role models.UserRole

		// 1. Try logging in as Faculty/Admin
		err := pool.QueryRow(c.Context(),
			`SELECT id, password_hash, role FROM faculty WHERE lower(email)=$1`,
			email).Scan(&userID, &hash, &role)

		if err == nil {
			if !hash.Valid || !BcryptVerify(hash.String, b.Password) {
				return fiber.NewError(fiber.StatusUnauthorized, "Invalid credentials")
			}
			return issueTokens(c, pool, userID, role)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err // Actual DB error
		}

		// 2. If not Faculty/Admin, try logging in as Volunteer
		err = pool.QueryRow(c.Context(),
			`SELECT id, password_hash, role FROM volunteers WHERE lower(email)=$1`,
			email).Scan(&userID, &hash, &role)

		if err == nil {
			if !hash.Valid || !BcryptVerify(hash.String, b.Password) {
				return fiber.NewError(fiber.StatusUnauthorized, "Invalid credentials or password not set for this account.")
			}
			return issueTokens(c, pool, userID, role)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err // Actual DB error
		}

		return fiber.NewError(fiber.StatusUnauthorized, "Invalid credentials")
	}
}

// Helper to issue JWT tokens after successful login
func issueTokens(c *fiber.Ctx, pool *pgxpool.Pool, userID int64, role models.UserRole) error {
	accessTTL := ttlFromEnv("ACCESS_TOKEN_TTL", 15*time.Minute)

	accessToken, err := mw.BuildAccessToken(userID, role, accessTTL)
	if err != nil {
		return fmt.Errorf("failed to build access token: %w", err)
	}

	response := models.LoginResponse{
		AccessToken: accessToken,
		ExpiresIn:   int(accessTTL.Seconds()),
		Role:        role,
		UserID:      userID,
	}

	// Only issue refresh token for Faculty/Admin roles, tied to the 'faculty' table
	if role == models.UserRoleAdmin || role == models.UserRoleFaculty {
		refreshTTL := ttlFromEnv("REFRESH_TOKEN_TTL", 30*24*time.Hour)

		rawRefreshToken := base64.StdEncoding.EncodeToString([]byte(strconv.FormatInt(time.Now().UnixNano(), 10) + "|" + strconv.FormatInt(userID, 10) + "|" + string(role)))
		refreshHash := sha256b64(rawRefreshToken)

		_, err = pool.Exec(c.Context(), `
			INSERT INTO auth_sessions(faculty_id, refresh_token_hash, user_agent, ip, expires_at)
			VALUES ($1,$2,$3,$4, NOW() + $5::interval)
		`, userID, refreshHash, c.Get("User-Agent"), c.IP(), refreshTTL.String())
		if err != nil {
			return fmt.Errorf("failed to store refresh token: %w", err)
		}
		response.RefreshToken = &rawRefreshToken
	}

	return c.JSON(response)
}

// ---------- /auth/register/volunteer (Student Self-Registration) ----------
// UPDATED: This function now handles setting a password for pre-registered volunteers.
func registerVolunteer(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var b models.RegisterVolunteerRequest
		if err := c.BodyParser(&b); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Bad JSON")
		}

		email := strings.ToLower(strings.TrimSpace(b.Email))
		name := strings.TrimSpace(b.Name)
		password := b.Password

		if name == "" || email == "" || password == "" || len(password) < 8 {
			return fiber.NewError(fiber.StatusBadRequest, "Name, valid email, and password (min 8 chars) are required")
		}

		// Hash the new password once
		hashedPassword, err := BcryptHash(password)
		if err != nil {
			return err
		}

		// 1. Check if email exists in faculty table (always a conflict for volunteer registration)
		var facultyExists bool
		err = pool.QueryRow(c.Context(), `SELECT EXISTS(SELECT 1 FROM faculty WHERE lower(email) = $1)`, email).Scan(&facultyExists)
		if err != nil {
			return fmt.Errorf("failed to check existing faculty email: %w", err)
		}
		if facultyExists {
			return fiber.NewError(fiber.StatusConflict, "Email already registered as a faculty member. Cannot register as volunteer.")
		}

		// 2. Check if email exists in volunteers table
		var volunteerID int64
		var existingPasswordHash sql.NullString
		err = pool.QueryRow(c.Context(), `SELECT id, password_hash FROM volunteers WHERE lower(email) = $1`, email).Scan(&volunteerID, &existingPasswordHash)

		if err == nil {
			// Email exists in volunteers table
			if existingPasswordHash.Valid {
				// 2a. Password already set for this volunteer. They should log in.
				return fiber.NewError(fiber.StatusConflict, "Email already registered as a volunteer with a password. Please login.")
			} else {
				// 2b. Email exists, but no password is set. Allow them to set it (claim the account).
				cmd, updateErr := pool.Exec(c.Context(), `
					UPDATE volunteers SET
						name = $1, email = $2, phone = $3, dept = $4, college_id = $5,
						password_hash = $6 -- Only update password_hash and potentially other profile data
					WHERE id = $7 AND role = $8 -- Ensure we only update volunteer roles
				`, name, email, b.Phone, b.Dept, b.CollegeID, hashedPassword, volunteerID, models.UserRoleVolunteer)
				if updateErr != nil {
					// Handle unique constraint violations if any field other than email is updated to a conflicting value
					if strings.Contains(updateErr.Error(), "volunteers_college_id_key") {
						return fiber.NewError(fiber.StatusConflict, "College ID already registered for another volunteer.")
					}
					return fmt.Errorf("failed to update existing volunteer with password: %w", updateErr)
				}
				if cmd.RowsAffected() == 0 {
					return fiber.NewError(fiber.StatusNotFound, "Volunteer not found or role mismatch (concurrent modification?)")
				}
				return c.Status(fiber.StatusOK).JSON(fiber.Map{"message": "Volunteer password set successfully for existing account", "id": volunteerID})
			}
		} else if errors.Is(err, sql.ErrNoRows) {
			// 3. Email does NOT exist in either faculty or volunteers table. Proceed with new registration.
			err = pool.QueryRow(c.Context(), `
				INSERT INTO volunteers(name, email, phone, dept, college_id, password_hash, role)
				VALUES ($1, $2, $3, $4, $5, $6, $7)
				RETURNING id
			`, name, email, b.Phone, b.Dept, b.CollegeID, hashedPassword, models.UserRoleVolunteer).Scan(&volunteerID)
			if err != nil {
				if strings.Contains(err.Error(), "volunteers_college_id_key") { // Check for unique constraint violation
					return fiber.NewError(fiber.StatusConflict, "College ID already registered.")
				}
				return fmt.Errorf("failed to insert new volunteer: %w", err)
			}
			return c.Status(fiber.StatusCreated).JSON(fiber.Map{"message": "Volunteer registered successfully", "id": volunteerID})
		} else {
			// Actual DB error during the SELECT query
			return fmt.Errorf("failed to check existing volunteer email: %w", err)
		}
	}
}

// ---------- /auth/refresh (Faculty/Admin only) ----------
func refresh(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var b models.RefreshRequest
		if err := c.BodyParser(&b); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Bad JSON")
		}
		if strings.TrimSpace(b.RefreshToken) == "" {
			return fiber.NewError(fiber.StatusBadRequest, "Refresh token required")
		}

		hashR := sha256b64(b.RefreshToken)
		var userID int64
		var role models.UserRole
		var expires time.Time
		var revoked *time.Time
		err := pool.QueryRow(c.Context(), `
			SELECT s.faculty_id, f.role, s.expires_at, s.revoked_at
			FROM auth_sessions s
			JOIN faculty f ON f.id = s.faculty_id
			WHERE s.refresh_token_hash = $1
			LIMIT 1
		`, hashR).Scan(&userID, &role, &expires, &revoked)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fiber.NewError(fiber.StatusUnauthorized, "Invalid refresh token")
			}
			return err
		}
		if revoked != nil || time.Now().After(expires) {
			if revoked == nil {
				_, _ = pool.Exec(c.Context(), `UPDATE auth_sessions SET revoked_at=NOW() WHERE refresh_token_hash=$1`, hashR)
			}
			return fiber.NewError(fiber.StatusUnauthorized, "Expired or revoked refresh token")
		}

		// Rotate refresh: revoke old & issue new
		_, _ = pool.Exec(c.Context(), `UPDATE auth_sessions SET revoked_at=NOW() WHERE refresh_token_hash=$1`, hashR)

		return issueTokens(c, pool, userID, role)
	}
}

// ---------- /auth/me ----------
func me() fiber.Handler {
	return func(c *fiber.Ctx) error {
		cls, _ := c.Locals("claims").(*mw.Claims)
		if cls == nil {
			return fiber.NewError(fiber.StatusUnauthorized)
		}
		return c.JSON(fiber.Map{"user_id": cls.Sub, "role": cls.Role})
	}
}

// ---------- /auth/logout ----------
func logout(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var b models.RefreshRequest
		if c.BodyParser(&b) == nil && strings.TrimSpace(b.RefreshToken) != "" {
			_, _ = pool.Exec(c.Context(), `UPDATE auth_sessions SET revoked_at=NOW() WHERE refresh_token_hash=$1`,
				sha256b64(b.RefreshToken))
		}
		return c.SendStatus(fiber.StatusNoContent)
	}
}

// ---------- /auth/register/faculty (admin-only) ----------
func registerFaculty(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var b models.RegisterFacultyRequest
		if err := c.BodyParser(&b); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Bad JSON")
		}
		if strings.TrimSpace(b.Name) == "" || strings.TrimSpace(b.Email) == "" || len(b.Password) < 8 {
			return fiber.NewError(fiber.StatusBadRequest, "Name, email, and password (>=8 chars) required")
		}
		hash, err := BcryptHash(b.Password)
		if err != nil {
			return err
		}
		role := models.UserRoleFaculty
		if b.Role != nil {
			r := *b.Role
			if r == models.UserRoleAdmin || r == models.UserRoleFaculty {
				role = r
			} else {
				return fiber.NewError(fiber.StatusBadRequest, "Invalid role specified for faculty registration")
			}
		}

		// Check for email collision with volunteers
		var exists int
		err = pool.QueryRow(c.Context(), `
			SELECT 1 FROM volunteers WHERE lower(email) = $1
		`, strings.ToLower(b.Email)).Scan(&exists)
		if err == nil {
			return fiber.NewError(fiber.StatusConflict, "Email already registered as a volunteer")
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err // Actual DB error
		}

		_, err = pool.Exec(c.Context(),
			`INSERT INTO faculty(name, email, password_hash, role) VALUES ($1,$2,$3,$4)`,
			b.Name, strings.ToLower(b.Email), hash, role)
		if err != nil {
			if strings.Contains(err.Error(), "faculty_email_key") {
				return fiber.NewError(fiber.StatusConflict, "Email already registered for a faculty account")
			}
			return err
		}
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"message": "Faculty account created successfully"})
	}
}
