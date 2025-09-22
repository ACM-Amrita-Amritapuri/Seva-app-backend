package health

import (
	"github.com/gofiber/fiber/v2"
)

func Health() fiber.Handler {
	return func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok", "message": "API is running"})
	}
}
