package announcements

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	mw "Seva-app-backend/middleware"
	"Seva-app-backend/models" // Using models.ErrorResponse and other models
)

// Register mounts announcement routes under /announcements
func Register(g fiber.Router, pool *pgxpool.Pool, jwtGuard fiber.Handler, requireAdmin fiber.Handler, requireVolunteer fiber.Handler) {
	// Admin/Faculty Reads (list all, get by ID)
	// g.Get("/", jwtGuard, mw.RequireRole(string(mw.RoleFaculty), string(mw.RoleAdmin)), ListAll(pool)) // Faculty/Admin can list all announcements
	// g.Get("/:id", jwtGuard, mw.RequireRole(string(mw.RoleFaculty), string(mw.RoleAdmin)), Get(pool))
	g.Get("/", jwtGuard, mw.RequireRole(string(models.UserRoleFaculty), string(models.UserRoleAdmin)), ListAll(pool))
	g.Get("/:id", jwtGuard, mw.RequireRole(string(models.UserRoleFaculty), string(models.UserRoleAdmin)), Get(pool))
	// Volunteer Read (list only relevant announcements)
	g.Get("/me", jwtGuard, requireVolunteer, ListForVolunteer(pool))

	// Admin Writes (protected by JWT and Admin role)
	g.Post("/", jwtGuard, requireAdmin, Create(pool))
	g.Put("/:id", jwtGuard, requireAdmin, Update(pool))
	g.Delete("/:id", jwtGuard, requireAdmin, Del(pool))
}

// listAll (Admin/Faculty) - GET /announcements?event_id=&committee_id=&active_only=true&limit=&offset=
func ListAll(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		eventID, err := strconv.ParseInt(c.Query("event_id", ""), 10, 64)
		if err != nil && c.Query("event_id", "") != "" { // Allow empty event_id to list all
			return fiber.NewError(fiber.StatusBadRequest, "invalid event_id")
		}
		committeeID, _ := strconv.ParseInt(c.Query("committee_id", "0"), 10, 64)
		activeOnly := strings.ToLower(c.Query("active_only", "false")) == "true"
		limit := clampInt(c.QueryInt("limit", 100), 1, 500)
		offset := maxInt(c.QueryInt("offset", 0), 0)

		args := []any{}
		where := []string{}
		paramCounter := 1

		if eventID > 0 {
			where = append(where, "a.event_id=$"+strconv.Itoa(paramCounter))
			args = append(args, eventID)
			paramCounter++
		}
		if committeeID > 0 {
			// Filter for specific committee OR event-wide if a committee is requested but can also see general
			// This logic is tricky. If you filter by committee_id, usually you only want *that* committee's.
			// If you want both general and specific committee, you'd need a more complex OR.
			// For ListAll, let's assume filtering by committee_id means "only that committee".
			where = append(where, "a.committee_id=$"+strconv.Itoa(paramCounter))
			args = append(args, committeeID)
			paramCounter++
		}
		if activeOnly {
			where = append(where, "(a.expires_at IS NULL OR a.expires_at > NOW())")
		}

		whereClause := ""
		if len(where) > 0 {
			whereClause = "WHERE " + strings.Join(where, " AND ")
		}

		order := `
		  ORDER BY CASE a.priority
		             WHEN 'urgent' THEN 1
		             WHEN 'high'   THEN 2
		             WHEN 'normal' THEN 3
		             ELSE 4
		           END, a.created_at DESC
		`

		args = append(args, limit, offset)
		query := `
		  SELECT a.id, a.event_id, a.committee_id, a.title, a.body,
		         a.priority::text, a.created_by, a.created_at, a.expires_at,
		         f.name AS created_by_name, c.name AS committee_name
		  FROM announcements a
		  LEFT JOIN faculty f ON f.id = a.created_by
		  LEFT JOIN committees c ON c.id = a.committee_id
		  ` + whereClause + order + `
		  LIMIT $` + strconv.Itoa(paramCounter) + ` OFFSET $` + strconv.Itoa(paramCounter+1)

		rows, err := pool.Query(c.Context(), query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()

		out := make([]models.Announcement, 0, limit)
		for rows.Next() {
			var a models.Announcement
			var priorityStr string // To scan the ENUM as text
			if err := rows.Scan(&a.ID, &a.EventID, &a.CommitteeID, &a.Title, &a.Body,
				&priorityStr, &a.CreatedBy, &a.CreatedAt, &a.ExpiresAt,
				&a.CreatedByName, &a.CommitteeName); err != nil {
				return err
			}
			a.Priority = models.AnnouncementPriority(priorityStr)
			out = append(out, a)
		}
		return c.JSON(out)
	}
}

// listForVolunteer (Volunteer) - GET /announcements/me
// Lists announcements relevant to the logged-in volunteer (event-wide AND committee-specific to their assignments).
func ListForVolunteer(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		volunteerID, err := mw.GetUserIDFromClaims(c)
		if err != nil {
			return fiber.NewError(fiber.StatusUnauthorized, "volunteer ID not found in token")
		}

		activeOnly := strings.ToLower(c.Query("active_only", "true")) == "true" // Default to active only for volunteers
		limit := clampInt(c.QueryInt("limit", 100), 1, 500)
		offset := maxInt(c.QueryInt("offset", 0), 0)

		// 1. Get all unique event_ids and committee_ids associated with the volunteer
		var assignedEventIDs []int64
		var assignedCommitteeIDs []int64

		rows, err := pool.Query(c.Context(), `
			SELECT DISTINCT event_id, committee_id
			FROM volunteer_assignments
			WHERE volunteer_id = $1
		`, volunteerID)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var eventID, committeeID int64
			if err := rows.Scan(&eventID, &committeeID); err != nil {
				return err
			}
			assignedEventIDs = append(assignedEventIDs, eventID)
			assignedCommitteeIDs = append(assignedCommitteeIDs, committeeID)
		}

		// If the volunteer has no assignments, return empty list
		if len(assignedEventIDs) == 0 {
			return c.JSON([]models.Announcement{})
		}

		// Remove duplicate event IDs
		uniqueEventIDs := make(map[int64]struct{})
		for _, id := range assignedEventIDs {
			uniqueEventIDs[id] = struct{}{}
		}
		finalEventIDs := make([]int64, 0, len(uniqueEventIDs))
		for id := range uniqueEventIDs {
			finalEventIDs = append(finalEventIDs, id)
		}

		// Remove duplicate committee IDs (optional, but good for cleaner query if array processing is slow)
		uniqueCommitteeIDs := make(map[int64]struct{})
		for _, id := range assignedCommitteeIDs {
			uniqueCommitteeIDs[id] = struct{}{}
		}
		finalCommitteeIDs := make([]int64, 0, len(uniqueCommitteeIDs))
		for id := range uniqueCommitteeIDs {
			finalCommitteeIDs = append(finalCommitteeIDs, id)
		}

		// 2. Build the WHERE clause for announcements
		args := []any{}
		whereConditions := []string{}
		paramCounter := 1

		// Condition 1: Event-wide announcements for any of the volunteer's assigned events
		whereConditions = append(whereConditions, "(a.event_id = ANY($"+strconv.Itoa(paramCounter)+") AND a.committee_id IS NULL)")
		args = append(args, finalEventIDs)
		paramCounter++

		// Condition 2: Committee-specific announcements for any of the volunteer's assigned committees
		if len(finalCommitteeIDs) > 0 {
			whereConditions = append(whereConditions, "(a.committee_id = ANY($"+strconv.Itoa(paramCounter)+"))")
			args = append(args, finalCommitteeIDs)
			paramCounter++
		}

		if activeOnly {
			whereConditions = append(whereConditions, "(a.expires_at IS NULL OR a.expires_at > NOW())")
		}

		whereClause := "WHERE " + strings.Join(whereConditions, " OR ") // Use OR to combine event-wide and committee-specific

		order := `
		  ORDER BY CASE a.priority
		             WHEN 'urgent' THEN 1
		             WHEN 'high'   THEN 2
		             WHEN 'normal' THEN 3
		             ELSE 4
		           END, a.created_at DESC
		`

		args = append(args, limit, offset)
		query := `
		  SELECT a.id, a.event_id, a.committee_id, a.title, a.body,
		         a.priority::text, a.created_by, a.created_at, a.expires_at,
		         f.name AS created_by_name, c.name AS committee_name
		  FROM announcements a
		  LEFT JOIN faculty f ON f.id = a.created_by
		  LEFT JOIN committees c ON c.id = a.committee_id
		  ` + whereClause + order + `
		  LIMIT $` + strconv.Itoa(paramCounter) + ` OFFSET $` + strconv.Itoa(paramCounter+1)

		rows, err = pool.Query(c.Context(), query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()

		out := make([]models.Announcement, 0, limit)
		for rows.Next() {
			var a models.Announcement
			var priorityStr string
			if err := rows.Scan(&a.ID, &a.EventID, &a.CommitteeID, &a.Title, &a.Body,
				&priorityStr, &a.CreatedBy, &a.CreatedAt, &a.ExpiresAt,
				&a.CreatedByName, &a.CommitteeName); err != nil {
				return err
			}
			a.Priority = models.AnnouncementPriority(priorityStr)
			out = append(out, a)
		}
		return c.JSON(out)
	}
}

// GET /announcements/:id
func Get(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		id, err := strconv.ParseInt(c.Params("id"), 10, 64)
		if err != nil || id <= 0 {
			return fiber.NewError(fiber.StatusBadRequest, "invalid id")
		}
		var a models.Announcement
		var priorityStr string
		err = pool.QueryRow(c.Context(), `
		  SELECT a.id, a.event_id, a.committee_id, a.title, a.body,
		         a.priority::text, a.created_by, a.created_at, a.expires_at,
		         f.name AS created_by_name, c.name AS committee_name
		  FROM announcements a
		  LEFT JOIN faculty f ON f.id = a.created_by
		  LEFT JOIN committees c ON c.id = a.committee_id
		  WHERE a.id=$1
		`, id).Scan(&a.ID, &a.EventID, &a.CommitteeID, &a.Title, &a.Body, &priorityStr, &a.CreatedBy, &a.CreatedAt, &a.ExpiresAt, &a.CreatedByName, &a.CommitteeName)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fiber.NewError(fiber.StatusNotFound, "not found")
			}
			return err
		}
		a.Priority = models.AnnouncementPriority(priorityStr)
		return c.JSON(a)
	}
}

// POST /announcements  (guarded by admin)
func Create(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var b models.CreateAnnouncementRequest
		if err := c.BodyParser(&b); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "bad json")
		}
		if b.EventID <= 0 || strings.TrimSpace(b.Title) == "" || strings.TrimSpace(b.Body) == "" {
			return fiber.NewError(fiber.StatusBadRequest, "event_id, title and body are required")
		}
		pr := normPriority(string(b.Priority))

		claims := c.Locals("claims").(*mw.Claims)
		createdBy := &claims.Sub // Set created_by to the ID of the logged-in admin/faculty

		var a models.Announcement
		var priorityStr string
		err := pool.QueryRow(c.Context(), `
		  INSERT INTO announcements(event_id, committee_id, title, body, priority, created_by, expires_at)
		  VALUES ($1,$2,$3,$4,$5::announcement_priority,$6,$7)
		  RETURNING id, event_id, committee_id, title, body,
		            priority::text, created_by, created_at, expires_at
		`, b.EventID, b.CommitteeID, b.Title, b.Body, pr, createdBy, b.ExpiresAt).
			Scan(&a.ID, &a.EventID, &a.CommitteeID, &a.Title, &a.Body, &priorityStr, &a.CreatedBy, &a.CreatedAt, &a.ExpiresAt)
		if err != nil {
			return err
		}
		a.Priority = models.AnnouncementPriority(priorityStr)
		return c.Status(fiber.StatusCreated).JSON(a)
	}
}

// PUT /announcements/:id  (guarded by admin)
func Update(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		id, err := strconv.ParseInt(c.Params("id"), 10, 64)
		if err != nil || id <= 0 {
			return fiber.NewError(fiber.StatusBadRequest, "invalid id")
		}
		var b models.UpdateAnnouncementRequest
		if err := c.BodyParser(&b); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "bad json")
		}
		sets := []string{}
		args := []any{}
		i := 1

		if b.Title != nil {
			t := strings.TrimSpace(*b.Title)
			if t == "" {
				return fiber.NewError(fiber.StatusBadRequest, "title cannot be empty")
			}
			sets = append(sets, "title=$"+itoa(i))
			args = append(args, t)
			i++
		}
		if b.Body != nil {
			body := strings.TrimSpace(*b.Body)
			if body == "" {
				return fiber.NewError(fiber.StatusBadRequest, "body cannot be empty")
			}
			sets = append(sets, "body=$"+itoa(i))
			args = append(args, body)
			i++
		}
		if b.Priority != nil {
			sets = append(sets, "priority=$"+itoa(i)+`::announcement_priority`)
			args = append(args, normPriority(string(*b.Priority)))
			i++
		}
		if b.CommitteeID != nil {
			sets = append(sets, "committee_id=$"+itoa(i))
			args = append(args, *b.CommitteeID)
			i++
		}
		if b.ExpiresAt != nil {
			sets = append(sets, "expires_at=$"+itoa(i))
			args = append(args, *b.ExpiresAt)
			i++
		}
		if len(sets) == 0 {
			return fiber.NewError(fiber.StatusBadRequest, "no fields to update")
		}
		args = append(args, id)

		sqlQuery := `UPDATE announcements SET ` + strings.Join(sets, ", ") + ` WHERE id=$` + itoa(i)
		cmd, err := pool.Exec(c.Context(), sqlQuery, args...)
		if err != nil {
			return err
		}
		if cmd.RowsAffected() == 0 {
			return fiber.NewError(fiber.StatusNotFound, "not found")
		}
		return c.SendStatus(fiber.StatusNoContent)
	}
}

// DELETE /announcements/:id  (guarded by admin)
func Del(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		id, err := strconv.ParseInt(c.Params("id"), 10, 64)
		if err != nil || id <= 0 {
			return fiber.NewError(fiber.StatusBadRequest, "invalid id")
		}
		cmd, err := pool.Exec(c.Context(), `DELETE FROM announcements WHERE id=$1`, id)
		if err != nil {
			return err
		}
		if cmd.RowsAffected() == 0 {
			return fiber.NewError(fiber.StatusNotFound, "not found")
		}
		return c.SendStatus(fiber.StatusNoContent)
	}
}

// ---- helpers ----
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
func itoa(i int) string { return strconv.FormatInt(int64(i), 10) }
func normPriority(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "urgent", "high", "normal", "low":
		return strings.ToLower(strings.TrimSpace(p))
	default:
		return "normal"
	}
}
