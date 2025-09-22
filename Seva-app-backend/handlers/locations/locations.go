package locations

import (
	"context"
	"database/sql"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"Seva-app-backend/models" // Using models.ErrorResponse and other models
)

// Register mounts location routes under /locations
func Register(g fiber.Router, pool *pgxpool.Pool, jwtGuard fiber.Handler, requireAdmin fiber.Handler) {
	// Public read access (anyone can list/get locations, perhaps for event maps)
	g.Get("/", ListLocations(pool))
	g.Get("/:id", GetLocationByID(pool))

	// Admin-only write access
	g.Post("/", jwtGuard, requireAdmin, CreateLocation(pool))
	g.Put("/:id", jwtGuard, requireAdmin, UpdateLocation(pool))
	g.Delete("/:id", jwtGuard, requireAdmin, DeleteLocation(pool))
}

// CreateLocation - POST /locations (Admin-only)
func CreateLocation(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		req := new(models.CreateLocationRequest)
		if err := c.BodyParser(req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse{Error: "Invalid request body"})
		}

		if req.EventID == 0 || req.Name == "" || req.Type == "" || req.Lat == 0 || req.Lng == 0 {
			return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse{Error: "Event ID, name, type, latitude, and longitude are required"})
		}

		ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
		defer cancel()

		var newLocation models.Location
		err := pool.QueryRow(ctx, `
			INSERT INTO locations (event_id, name, type, description, lat, lng)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id, event_id, name, type, description, lat, lng
		`, req.EventID, req.Name, req.Type, req.Description, req.Lat, req.Lng).Scan(
			&newLocation.ID, &newLocation.EventID, &newLocation.Name, &newLocation.Type,
			&newLocation.Description, &newLocation.Lat, &newLocation.Lng,
		)
		if err != nil {
			log.Printf("Error creating location: %v", err)
			if strings.Contains(err.Error(), "locations_event_id_name_key") { // Check for unique constraint violation
				return fiber.NewError(fiber.StatusConflict, "Location name already exists for this event")
			}
			return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse{Error: "Failed to create location"})
		}

		return c.Status(fiber.StatusCreated).JSON(newLocation)
	}
}

// ListLocations - GET /locations?event_id= (Public)
func ListLocations(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
		defer cancel()

		eventIDStr := c.Query("event_id")
		var eventID sql.NullInt64 // Use NullInt64 to correctly handle NULL for $1
		if eventIDStr != "" {
			id, err := strconv.ParseInt(eventIDStr, 10, 64)
			if err != nil {
				return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse{Error: "Invalid event_id query parameter"})
			}
			eventID = sql.NullInt64{Int64: id, Valid: true}
		}

		var locations []models.Location
		query := `
			SELECT id, event_id, name, type, description, lat, lng
			FROM locations
			WHERE ($1::BIGINT IS NULL OR event_id = $1)
			ORDER BY name ASC
		`
		rows, err := pool.Query(ctx, query, eventID)
		if err != nil {
			log.Printf("Error querying locations: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse{Error: "Failed to retrieve locations"})
		}
		defer rows.Close()

		for rows.Next() {
			var location models.Location
			err := rows.Scan(
				&location.ID, &location.EventID, &location.Name, &location.Type,
				&location.Description, &location.Lat, &location.Lng,
			)
			if err != nil {
				log.Printf("Error scanning location row: %v", err)
				return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse{Error: "Failed to process location data"})
			}
			locations = append(locations, location)
		}

		if err := rows.Err(); err != nil {
			log.Printf("Error iterating location rows: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse{Error: "Failed to retrieve locations"})
		}

		return c.JSON(locations)
	}
}

// GetLocationByID - GET /locations/:id (Public)
func GetLocationByID(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		locationIDStr := c.Params("id")
		locationID, err := strconv.ParseInt(locationIDStr, 10, 64)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse{Error: "Invalid location ID"})
		}

		ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
		defer cancel()

		var location models.Location
		err = pool.QueryRow(ctx, `
			SELECT id, event_id, name, type, description, lat, lng
			FROM locations WHERE id = $1
		`, locationID).Scan(
			&location.ID, &location.EventID, &location.Name, &location.Type,
			&location.Description, &location.Lat, &location.Lng,
		)
		if err != nil {
			if err == pgx.ErrNoRows {
				return c.Status(fiber.StatusNotFound).JSON(models.ErrorResponse{Error: "Location not found"})
			}
			log.Printf("Error querying location by ID %d: %v", locationID, err)
			return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse{Error: "Failed to retrieve location"})
		}

		return c.JSON(location)
	}
}

// UpdateLocation - PUT /locations/:id (Admin-only)
func UpdateLocation(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		locationIDStr := c.Params("id")
		locationID, err := strconv.ParseInt(locationIDStr, 10, 64)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse{Error: "Invalid location ID"})
		}

		req := new(models.UpdateLocationRequest)
		if err := c.BodyParser(req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse{Error: "Invalid request body"})
		}

		updates := make(map[string]interface{})
		if req.Name != nil {
			updates["name"] = *req.Name
		}
		if req.Type != nil {
			updates["type"] = *req.Type
		}
		if req.Description != nil {
			updates["description"] = *req.Description
		}
		if req.Lat != nil {
			updates["lat"] = *req.Lat
		}
		if req.Lng != nil {
			updates["lng"] = *req.Lng
		}

		if len(updates) == 0 {
			return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse{Error: "No fields provided for update"})
		}

		ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
		defer cancel()

		var (
			setClauses []string
			args       []interface{}
			i          = 1
		)
		for key, val := range updates {
			setClauses = append(setClauses, key+"=$"+strconv.Itoa(i))
			args = append(args, val)
			i++
		}
		args = append(args, locationID) // The last argument is for the WHERE clause

		query := "UPDATE locations SET " + strings.Join(setClauses, ", ") + " WHERE id = $" + strconv.Itoa(i) + " RETURNING id"
		cmdTag, err := pool.Exec(ctx, query, args...)
		if err != nil {
			log.Printf("Error updating location %d: %v", locationID, err)
			if strings.Contains(err.Error(), "locations_event_id_name_key") {
				return fiber.NewError(fiber.StatusConflict, "Location name already exists for this event")
			}
			return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse{Error: "Failed to update location"})
		}
		if cmdTag.RowsAffected() == 0 {
			return c.Status(fiber.StatusNotFound).JSON(models.ErrorResponse{Error: "Location not found"})
		}

		return c.JSON(fiber.Map{"message": "Location updated successfully", "id": locationID})
	}
}

// DeleteLocation - DELETE /locations/:id (Admin-only)
func DeleteLocation(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		locationIDStr := c.Params("id")
		locationID, err := strconv.ParseInt(locationIDStr, 10, 64)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse{Error: "Invalid location ID"})
		}

		ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
		defer cancel()

		cmdTag, err := pool.Exec(ctx, `DELETE FROM locations WHERE id = $1`, locationID)
		if err != nil {
			log.Printf("Error deleting location %d: %v", locationID, err)
			return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse{Error: "Failed to delete location"})
		}

		if cmdTag.RowsAffected() == 0 {
			return c.Status(fiber.StatusNotFound).JSON(models.ErrorResponse{Error: "Location not found"})
		}

		return c.JSON(fiber.Map{"message": "Location deleted successfully", "id": locationID})
	}
}
