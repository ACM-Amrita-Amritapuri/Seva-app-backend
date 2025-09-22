package committees

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"Seva-app-backend/models" // Ensure this import is present
)

// Register mounts committee routes under /committees
// ... (rest of the Register function remains the same as previous)
func Register(g fiber.Router, pool *pgxpool.Pool, jwtGuard fiber.Handler, requireAdmin fiber.Handler) {
	// Public read access (anyone can list/get committees, perhaps for event info)
	g.Get("/", List(pool))
	g.Get("/:id", Get(pool))

	// Admin-only write access
	g.Post("/", jwtGuard, requireAdmin, Create(pool))
	g.Put("/:id", jwtGuard, requireAdmin, Update(pool))
	g.Delete("/:id", jwtGuard, requireAdmin, Del(pool))
}

// List - GET /committees?event_id=1&limit=100&offset=0
// ... (rest of the List function remains the same as previous)
func List(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		eventIDStr := c.Query("event_id", "")
		limit := clampInt(c.QueryInt("limit", 100), 1, 500)
		offset := maxInt(c.QueryInt("offset", 0), 0)
		args := []any{}
		where := ""
		paramCounter := 1

		if eventIDStr != "" {
			eventID64, err := strconv.ParseInt(eventIDStr, 10, 64)
			if err != nil {
				return fiber.NewError(fiber.StatusBadRequest, "invalid event_id")
			}
			where = "WHERE c.event_id = $" + strconv.Itoa(paramCounter)
			args = append(args, eventID64)
			paramCounter++
		}

		query := `
			SELECT c.id, c.event_id, c.name, COALESCE(c.description,''), c.created_at, e.name as event_name
			FROM committees c
			JOIN events e ON e.id = c.event_id
			` + where + `
			ORDER BY c.name
			LIMIT $` + strconv.Itoa(paramCounter) + ` OFFSET $` + strconv.Itoa(paramCounter+1)

		args = append(args, limit, offset)

		rows, err := pool.Query(c.Context(), query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()

		out := make([]models.Committee, 0, limit)
		for rows.Next() {
			var cm models.Committee
			if err := rows.Scan(&cm.ID, &cm.EventID, &cm.Name, &cm.Description, &cm.CreatedAt, &cm.EventName); err != nil {
				return err
			}
			out = append(out, cm)
		}
		return c.JSON(out)
	}
}

// Get - GET /committees/:id
// ... (rest of the Get function remains the same as previous)
func Get(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		id, err := strconv.ParseInt(c.Params("id"), 10, 64)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "invalid id")
		}
		var cm models.Committee
		err = pool.
			QueryRow(c.Context(),
				`SELECT c.id, c.event_id, c.name, COALESCE(c.description,''), c.created_at, e.name as event_name
				 FROM committees c
				 JOIN events e ON e.id = c.event_id
				 WHERE c.id=$1`, id).
			Scan(&cm.ID, &cm.EventID, &cm.Name, &cm.Description, &cm.CreatedAt, &cm.EventName)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fiber.NewError(fiber.StatusNotFound, "committee not found")
			}
			return err
		}
		return c.JSON(cm)
	}
}

// Create - POST /committees (Admin-only)
func Create(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var b models.CreateCommitteeRequest // This was the undeclared name
		if err := c.BodyParser(&b); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "bad json")
		}
		if b.EventID <= 0 || len(b.Name) == 0 {
			return fiber.NewError(fiber.StatusBadRequest, "event_id and name are required")
		}
		desc := ""
		if b.Description != nil {
			desc = *b.Description
		}

		var cm models.Committee
		err := pool.
			QueryRow(c.Context(),
				`INSERT INTO committees(event_id, name, description)
				 VALUES ($1,$2,$3)
				 RETURNING id, event_id, name, COALESCE(description,''), created_at`,
				b.EventID, b.Name, desc).
			Scan(&cm.ID, &cm.EventID, &cm.Name, &cm.Description, &cm.CreatedAt)
		if err != nil {
			// unique(event_id, name) may trigger a constraint error
			if strings.Contains(err.Error(), "committees_event_id_name_key") { // Assuming you have such a constraint
				return fiber.NewError(fiber.StatusConflict, "Committee name already exists for this event")
			}
			return err
		}
		return c.Status(fiber.StatusCreated).JSON(cm)
	}
}

// Update - PUT /committees/:id (Admin-only)
func Update(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		id, err := strconv.ParseInt(c.Params("id"), 10, 64)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "invalid id")
		}
		var b models.UpdateCommitteeRequest // This was the undeclared name
		if err := c.BodyParser(&b); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "bad json")
		}
		if b.Name == nil && b.Description == nil {
			return fiber.NewError(fiber.StatusBadRequest, "no fields to update")
		}

		// Build dynamic SET clause
		set := ""
		args := []any{}
		i := 1
		if b.Name != nil {
			set += "name = $" + strconv.Itoa(i)
			args = append(args, *b.Name)
			i++
		}
		if b.Description != nil {
			if set != "" {
				set += ", "
			}
			set += "description = $" + strconv.Itoa(i)
			args = append(args, *b.Description)
			i++
		}
		args = append(args, id)

		cmd, err := pool.Exec(c.Context(),
			`UPDATE committees SET `+set+` WHERE id = $`+strconv.Itoa(i), args...)
		if err != nil {
			// Check for unique constraint violation on name if it was updated
			if b.Name != nil && strings.Contains(err.Error(), "committees_event_id_name_key") {
				return fiber.NewError(fiber.StatusConflict, "Committee name already exists for this event")
			}
			return err
		}
		if cmd.RowsAffected() == 0 {
			return fiber.NewError(fiber.StatusNotFound, "committee not found")
		}
		return c.SendStatus(fiber.StatusNoContent)
	}
}

// Del - DELETE /committees/:id (Admin-only)
// ... (rest of the Del function remains the same as previous)
func Del(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		id, err := strconv.ParseInt(c.Params("id"), 10, 64)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "invalid id")
		}
		cmd, err := pool.Exec(c.Context(), `DELETE FROM committees WHERE id=$1`, id)
		if err != nil {
			return err
		}
		if cmd.RowsAffected() == 0 {
			return fiber.NewError(fiber.StatusNotFound, "committee not found")
		}
		return c.SendStatus(fiber.StatusNoContent)
	}
}

// helpers (moved to common/utils or kept local)
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
