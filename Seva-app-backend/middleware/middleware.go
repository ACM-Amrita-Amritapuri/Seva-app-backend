package middleware

import (
	"errors"
	"os"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v4"

	"Seva-app-backend/models" // Import models package
)

// Claims structure for JWT
type Claims struct {
	Sub  int64           `json:"sub"`  // User ID (faculty.id or volunteer.id)
	Role models.UserRole `json:"role"` // Use models.UserRole
	jwt.RegisteredClaims
}

// JwtGuard is a middleware to validate JWT access tokens.
func JwtGuard() fiber.Handler {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		return func(c *fiber.Ctx) error {
			return fiber.NewError(fiber.StatusInternalServerError, "JWT_SECRET not configured")
		}
	}

	return func(c *fiber.Ctx) error {
		h := c.Get("Authorization")
		parts := strings.SplitN(h, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			return fiber.NewError(fiber.StatusUnauthorized, "Missing or malformed bearer token")
		}
		tkn, err := jwt.ParseWithClaims(parts[1], &Claims{}, func(t *jwt.Token) (any, error) {
			return []byte(secret), nil
		}, jwt.WithValidMethods([]string{"HS256"}))
		if err != nil || !tkn.Valid {
			return fiber.NewError(fiber.StatusUnauthorized, "Invalid token: "+err.Error())
		}
		c.Locals("claims", tkn.Claims.(*Claims)) // Store claims in context for downstream handlers
		return c.Next()
	}
}

// RequireRole is a middleware to check if the authenticated user has one of the allowed roles.
func RequireRole(roles ...string) fiber.Handler {
	allowed := map[models.UserRole]struct{}{} // Use models.UserRole
	for _, r := range roles {
		allowed[models.UserRole(r)] = struct{}{}
	}

	return func(c *fiber.Ctx) error {
		cls, ok := c.Locals("claims").(*Claims)
		if !ok || cls == nil {
			return fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
		}
		if _, ok := allowed[cls.Role]; !ok {
			return fiber.NewError(fiber.StatusForbidden, "Insufficient role privileges")
		}
		return c.Next()
	}
}

// BuildAccessToken Helper to build JWT access tokens.
func BuildAccessToken(sub int64, role models.UserRole, ttl time.Duration) (string, error) { // Use models.UserRole
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		return "", errors.New("JWT_SECRET environment variable is not set")
	}

	now := time.Now()
	claims := &Claims{
		Sub:  sub,
		Role: role, // Use models.UserRole
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// GetUserIDFromClaims extracts the user ID from the JWT claims in the Fiber context.
func GetUserIDFromClaims(c *fiber.Ctx) (int64, error) {
	cls, ok := c.Locals("claims").(*Claims)
	if !ok || cls == nil {
		return 0, fiber.NewError(fiber.StatusUnauthorized, "user claims not found")
	}
	return cls.Sub, nil
}

// GetUserRoleFromClaims extracts the user role from the JWT claims in the Fiber context.
func GetUserRoleFromClaims(c *fiber.Ctx) (models.UserRole, error) { // Return models.UserRole
	cls, ok := c.Locals("claims").(*Claims)
	if !ok || cls == nil {
		return "", fiber.NewError(fiber.StatusUnauthorized, "user claims not found")
	}
	return cls.Role, nil
}
