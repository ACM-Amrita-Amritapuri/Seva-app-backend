package volunteers

import (
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	hAuth "Seva-app-backend/handlers/auth" // For bcrypt functions
	mw "Seva-app-backend/middleware"
	"Seva-app-backend/models"
)

// Register mounts routes under /volunteers
func Register(g fiber.Router, pool *pgxpool.Pool, jwtGuard fiber.Handler, requireAdmin fiber.Handler, requireVolunteer fiber.Handler) {
	// --- Admin-only Volunteer Management ---
	g.Post("/", jwtGuard, requireAdmin, CreateSingle(pool))         // Admin creates a volunteer
	g.Get("/", jwtGuard, requireAdmin, ListVolunteers(pool))        // Admin lists all volunteers, now with committee filter
	g.Get("/:id", jwtGuard, requireAdmin, GetVolunteerByID(pool))   // Admin gets a volunteer by ID
	g.Put("/:id", jwtGuard, requireAdmin, UpdateVolunteer(pool))    // Admin updates a volunteer
	g.Delete("/:id", jwtGuard, requireAdmin, DeleteVolunteer(pool)) // Admin deletes a volunteer

	// --- Admin-only Bulk Operations ---
	g.Post("/bulk", jwtGuard, requireAdmin, BulkUpload(pool))                            // Admin bulk uploads volunteers
	g.Get("/export_csv", jwtGuard, requireAdmin, ExportVolunteersCSV(pool))              // Admin exports volunteers
	g.Get("/assignments/export_csv", jwtGuard, requireAdmin, ExportAssignmentsCSV(pool)) // Admin exports assignments

	// --- Admin-only Assignment Management ---
	g.Post("/assignments", jwtGuard, requireAdmin, CreateAssignment(pool))       // Admin creates a new assignment
	g.Get("/assignments", jwtGuard, requireAdmin, ListAssignments(pool))         // Admin lists all assignments, now with shift/date filters
	g.Get("/assignments/:id", jwtGuard, requireAdmin, GetAssignmentByID(pool))   // Admin gets an assignment by ID
	g.Put("/assignments/:id", jwtGuard, requireAdmin, UpdateAssignment(pool))    // Admin updates an assignment
	g.Delete("/assignments/:id", jwtGuard, requireAdmin, DeleteAssignment(pool)) // Admin deletes an assignment

	// --- Volunteer (student) Specific Routes ---
	g.Get("/me", jwtGuard, requireVolunteer, GetMyProfile(pool))
	g.Post("/me/set-password", jwtGuard, requireVolunteer, SetMyPassword(pool))
	g.Get("/me/assignments", jwtGuard, requireVolunteer, GetMyAssignments(pool)) // Now shows shift info
	g.Get("/me/committees", jwtGuard, requireVolunteer, GetMyCommittees(pool))
}

// --- Admin-Only Volunteer CRUD ---

// CreateSingle - POST /volunteers (Admin)
// Allows admin to create a new volunteer record.
func CreateSingle(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var b models.CreateVolunteerRequest
		if err := c.BodyParser(&b); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Bad JSON")
		}
		if strings.TrimSpace(b.Name) == "" {
			return fiber.NewError(fiber.StatusBadRequest, "Name is required")
		}
		if b.Email != nil && strings.TrimSpace(*b.Email) == "" {
			return fiber.NewError(fiber.StatusBadRequest, "Email cannot be empty if provided")
		}

		var passwordHash *string
		if b.Password != nil && *b.Password != "" {
			hash, err := hAuth.BcryptHash(*b.Password)
			if err != nil {
				return err
			}
			passwordHash = &hash
		}

		// Check if email already exists in faculty or volunteers table
		if b.Email != nil {
			var exists int
			err := pool.QueryRow(c.Context(), `
				SELECT 1 FROM faculty WHERE lower(email) = $1
				UNION ALL
				SELECT 1 FROM volunteers WHERE lower(email) = $1
			`, strings.ToLower(*b.Email)).Scan(&exists)

			if err == nil {
				return fiber.NewError(fiber.StatusConflict, "Email already registered")
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err // Actual DB error
			}
		}

		var vID int64
		err := pool.QueryRow(c.Context(), `
			INSERT INTO volunteers(name, email, phone, dept, college_id, password_hash, role)
			VALUES ($1,$2,$3,$4,$5,$6, $7)
			RETURNING id
		`, b.Name, b.Email, b.Phone, b.Dept, b.CollegeID, passwordHash, models.UserRoleVolunteer).Scan(&vID)
		if err != nil {
			if strings.Contains(err.Error(), "volunteers_college_id_key") {
				return fiber.NewError(fiber.StatusConflict, "Volunteer with this college ID already exists")
			}
			return err
		}

		return c.Status(fiber.StatusCreated).JSON(fiber.Map{
			"id": vID, "name": b.Name, "email": b.Email, "phone": b.Phone, "dept": b.Dept, "college_id": b.CollegeID,
		})
	}
}

// ListVolunteers - GET /volunteers?committee_id=&limit=100&offset=0 (Admin)
// Lists all volunteer records, with optional committee filter.
func ListVolunteers(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		limit := clampInt(c.QueryInt("limit", 100), 1, 500)
		offset := maxInt(c.QueryInt("offset", 0), 0)

		committeeIDFilter := sql.NullInt64{}
		committeeIDStr := c.Query("committee_id", "")
		if committeeIDStr != "" {
			if id, err := strconv.ParseInt(committeeIDStr, 10, 64); err == nil {
				committeeIDFilter = sql.NullInt64{Int64: id, Valid: true}
			} else {
				return fiber.NewError(fiber.StatusBadRequest, "invalid committee_id")
			}
		}

		args := []any{limit, offset}
		whereClause := ""
		if committeeIDFilter.Valid {
			whereClause = `
				JOIN volunteer_assignments va ON va.volunteer_id = v.id
				WHERE va.committee_id = $3
			`
			args = append(args, committeeIDFilter.Int64)
		}

		query := `
			SELECT v.id, v.name, v.email, v.phone, v.dept, v.college_id, v.created_at
			FROM volunteers v
			` + whereClause + `
			ORDER BY v.name
			LIMIT $1 OFFSET $2
		`

		rows, err := pool.Query(c.Context(), query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()

		out := make([]models.Volunteer, 0, limit)
		for rows.Next() {
			var v models.Volunteer
			if err := rows.Scan(&v.ID, &v.Name, &v.Email, &v.Phone, &v.Dept, &v.CollegeID, &v.CreatedAt); err != nil {
				return err
			}
			out = append(out, v)
		}
		return c.JSON(out)
	}
}

// GetVolunteerByID - GET /volunteers/:id (Admin)
func GetVolunteerByID(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		id, err := strconv.ParseInt(c.Params("id"), 10, 64)
		if err != nil || id <= 0 {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid volunteer ID")
		}

		var v models.Volunteer
		err = pool.QueryRow(c.Context(), `
			SELECT id, name, email, phone, dept, college_id, created_at
			FROM volunteers WHERE id = $1
		`, id).Scan(&v.ID, &v.Name, &v.Email, &v.Phone, &v.Dept, &v.CollegeID, &v.CreatedAt)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fiber.NewError(fiber.StatusNotFound, "Volunteer not found")
			}
			return err
		}
		return c.JSON(v)
	}
}

// UpdateVolunteer - PUT /volunteers/:id (Admin)
func UpdateVolunteer(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		id, err := strconv.ParseInt(c.Params("id"), 10, 64)
		if err != nil || id <= 0 {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid volunteer ID")
		}

		var b models.UpdateVolunteerRequest
		if err := c.BodyParser(&b); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Bad JSON")
		}

		sets := []string{}
		args := []any{}
		i := 1

		if b.Name != nil {
			name := strings.TrimSpace(*b.Name)
			if name == "" {
				return fiber.NewError(fiber.StatusBadRequest, "Name cannot be empty")
			}
			sets = append(sets, "name=$"+itoa(i))
			args = append(args, name)
			i++
		}
		if b.Email != nil {
			email := strings.TrimSpace(*b.Email)
			if email == "" {
				sets = append(sets, "email=$"+itoa(i))
				args = append(args, nil)
			} else {
				var existingUserID int64
				err = pool.QueryRow(c.Context(), `SELECT id FROM volunteers WHERE lower(email) = $1 AND id != $2`, email, id).Scan(&existingUserID)
				if err == nil {
					return fiber.NewError(fiber.StatusConflict, "Email already in use by another volunteer")
				}
				if !errors.Is(err, sql.ErrNoRows) {
					return err
				}
				err = pool.QueryRow(c.Context(), `SELECT id FROM faculty WHERE lower(email) = $1`, email).Scan(&existingUserID)
				if err == nil {
					return fiber.NewError(fiber.StatusConflict, "Email already in use by a faculty member")
				}
				if !errors.Is(err, sql.ErrNoRows) {
					return err
				}

				sets = append(sets, "email=$"+itoa(i))
				args = append(args, email)
			}
			i++
		}
		if b.Phone != nil {
			sets = append(sets, "phone=$"+itoa(i))
			args = append(args, nullable(strings.TrimSpace(*b.Phone)))
			i++
		}
		if b.Dept != nil {
			sets = append(sets, "dept=$"+itoa(i))
			args = append(args, nullable(strings.TrimSpace(*b.Dept)))
			i++
		}
		if b.CollegeID != nil {
			collegeID := strings.TrimSpace(*b.CollegeID)
			if collegeID == "" {
				sets = append(sets, "college_id=$"+itoa(i))
				args = append(args, nil)
			} else {
				var existingUserID int64
				err = pool.QueryRow(c.Context(), `SELECT id FROM volunteers WHERE college_id = $1 AND id != $2`, collegeID, id).Scan(&existingUserID)
				if err == nil {
					return fiber.NewError(fiber.StatusConflict, "College ID already in use by another volunteer")
				}
				if !errors.Is(err, sql.ErrNoRows) {
					return err
				}

				sets = append(sets, "college_id=$"+itoa(i))
				args = append(args, collegeID)
			}
			i++
		}
		if b.Password != nil {
			hash, err := hAuth.BcryptHash(*b.Password)
			if err != nil {
				return err
			}
			sets = append(sets, "password_hash=$"+itoa(i))
			args = append(args, hash)
			i++
		}
		if b.Role != nil {
			roleStr := strings.ToLower(string(*b.Role))
			if roleStr != string(models.UserRoleVolunteer) { // Only allow setting to 'volunteer' for volunteers
				return fiber.NewError(fiber.StatusBadRequest, "Invalid role for volunteer update. Can only be 'volunteer'.")
			}
			sets = append(sets, "role=$"+itoa(i)+`::user_role`)
			args = append(args, roleStr)
			i++
		}

		if len(sets) == 0 {
			return fiber.NewError(fiber.StatusBadRequest, "No fields to update")
		}
		args = append(args, id)

		sqlQuery := `UPDATE volunteers SET ` + strings.Join(sets, ", ") + ` WHERE id=$` + itoa(i)
		cmd, err := pool.Exec(c.Context(), sqlQuery, args...)
		if err != nil {
			if strings.Contains(err.Error(), "volunteers_email_key") {
				return fiber.NewError(fiber.StatusConflict, "Email already in use by another volunteer or faculty.")
			}
			if strings.Contains(err.Error(), "volunteers_college_id_key") {
				return fiber.NewError(fiber.StatusConflict, "College ID already in use by another volunteer.")
			}
			return err
		}
		if cmd.RowsAffected() == 0 {
			return fiber.NewError(fiber.StatusNotFound, "Volunteer not found")
		}
		return c.SendStatus(fiber.StatusNoContent)
	}
}

// DeleteVolunteer - DELETE /volunteers/:id (Admin)
func DeleteVolunteer(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		id, err := strconv.ParseInt(c.Params("id"), 10, 64)
		if err != nil || id <= 0 {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid volunteer ID")
		}
		cmd, err := pool.Exec(c.Context(), `DELETE FROM volunteers WHERE id=$1`, id)
		if err != nil {
			return err
		}
		if cmd.RowsAffected() == 0 {
			return fiber.NewError(fiber.StatusNotFound, "Volunteer not found")
		}
		return c.SendStatus(fiber.StatusNoContent)
	}
}
func createIndexer(headers []string) map[string]int {
	idx := make(map[string]int)
	for i, header := range headers {
		// Trim whitespace and create multiple variations
		cleanHeader := strings.TrimSpace(header)
		idx[cleanHeader] = i
		idx[strings.ToLower(cleanHeader)] = i
	}
	return idx
}

// --- Admin-Only Bulk Operations ---

// BulkUpload - POST /volunteers/bulk?event_id=1&committee_id=3 (Admin)
// CSV header: name,email,phone,dept,college_id,reporting_time_iso,shift,start_time_iso,end_time_iso,role,status,notes
func BulkUpload(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		eventID, err := strconv.ParseInt(c.Query("event_id", ""), 10, 64)
		if err != nil || eventID <= 0 {
			return fiber.NewError(fiber.StatusBadRequest, "event_id is required")
		}
		committeeID, err := strconv.ParseInt(c.Query("committee_id", ""), 10, 64)
		if err != nil || committeeID <= 0 {
			return fiber.NewError(fiber.StatusBadRequest, "committee_id is required")
		}

		formFile, err := c.FormFile("file")
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "file is required")
		}
		f, err := formFile.Open()
		if err != nil {
			return err
		}
		defer f.Close()

		rd := csv.NewReader(f)
		rd.FieldsPerRecord = -1

		// read header
		header, err := rd.Read()
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "empty or invalid csv")
		}
		fmt.Printf("Debug - CSV Headers: %v\n", header)
		idx := createIndexer(header)

		type rowErr struct {
			line int
			msg  string
		}
		var rowErrors []rowErr
		createdVols := 0
		createdAssigns := 0
		updatedAssigns := 0 // This needs to be actively incremented on ON CONFLICT DO UPDATE
		line := 1           // header

		tx, err := pool.Begin(c.Context())
		if err != nil {
			return err
		}
		defer tx.Rollback(c.Context())

		for {
			rec, err := rd.Read()
			if errors.Is(err, io.EOF) {
				break
			}
			line++
			if err != nil {
				rowErrors = append(rowErrors, rowErr{line, fmt.Sprintf("read error: %v", err)})
				continue
			}

			// Mandatory: name
			name := strings.TrimSpace(get(rec, idx, "name"))
			if name == "" {
				rowErrors = append(rowErrors, rowErr{line, "missing name"})
				continue
			}

			email := nullable(trim(get(rec, idx, "email")))
			phone := nullable(trim(get(rec, idx, "phone")))
			dept := nullable(trim(get(rec, idx, "dept")))
			collegeID := nullable(trim(get(rec, idx, "Roll No")))

			// Extract shift, group, and faculty coordinator
			shift := nullable(trim(get(rec, idx, "shift")))
			groupNo := trim(get(rec, idx, "Group No"))
			facultyCoordinator := trim(get(rec, idx, "Faculty"))
			var notesArray []string
			if groupNo != "" {
				notesArray = append(notesArray, "Group No: "+groupNo)
			}
			if facultyCoordinator != "" {
				notesArray = append(notesArray, "Faculty: "+facultyCoordinator)
			}

			var notes *string
			if len(notesArray) > 0 {
				notesStr := strings.Join(notesArray, ", ")
				notes = &notesStr
			}

			assignRole := strings.ToLower(defaultIfEmpty(trim(get(rec, idx, "role")), "volunteer"))
			assignStatus := strings.ToLower(defaultIfEmpty(trim(get(rec, idx, "status")), "assigned"))

			var rt, startTime, endTime *time.Time
			if iso := trim(get(rec, idx, "reporting_time_iso")); iso != "" {
				t, e := time.Parse(time.RFC3339, iso)
				if e != nil {
					rowErrors = append(rowErrors, rowErr{line, "bad reporting_time_iso (RFC3339)"})
					continue
				}
				rt = &t
			}
			if iso := trim(get(rec, idx, "start_time_iso")); iso != "" {
				t, e := time.Parse(time.RFC3339, iso)
				if e != nil {
					rowErrors = append(rowErrors, rowErr{line, "bad start_time_iso (RFC3339)"})
					continue
				}
				startTime = &t
			}
			if iso := trim(get(rec, idx, "end_time_iso")); iso != "" {
				t, e := time.Parse(time.RFC3339, iso)
				if e != nil {
					rowErrors = append(rowErrors, rowErr{line, "bad end_time_iso (RFC3339)"})
					continue
				}
				endTime = &t
			}

			var vID int64
			var existsAsFaculty bool

			// Try to find volunteer by email or college_id
			foundVolunteer := false
			if email != nil && *email != "" {
				err = tx.QueryRow(c.Context(), `SELECT id FROM volunteers WHERE lower(email)=$1`, *email).Scan(&vID)
				if err == nil {
					foundVolunteer = true
				} else if !errors.Is(err, sql.ErrNoRows) {
					rowErrors = append(rowErrors, rowErr{line, fmt.Sprintf("check existing volunteer by email: %v", err)})
					continue
				}
			}

			if !foundVolunteer && collegeID != nil && *collegeID != "" {
				err = tx.QueryRow(c.Context(), `SELECT id FROM volunteers WHERE college_id=$1`, *collegeID).Scan(&vID)
				if err == nil {
					foundVolunteer = true
				} else if !errors.Is(err, sql.ErrNoRows) {
					rowErrors = append(rowErrors, rowErr{line, fmt.Sprintf("check existing volunteer by college_id: %v", err)})
					continue
				}
			}

			// If not found, check if email/college_id conflicts with faculty
			if !foundVolunteer {
				if email != nil && *email != "" {
					err = tx.QueryRow(c.Context(), `SELECT 1 FROM faculty WHERE lower(email)=$1`, *email).Scan(&existsAsFaculty)
					if err == nil {
						existsAsFaculty = true
					} else if !errors.Is(err, sql.ErrNoRows) {
						rowErrors = append(rowErrors, rowErr{line, fmt.Sprintf("check existing faculty by email: %v", err)})
						continue
					}
					if existsAsFaculty {
						rowErrors = append(rowErrors, rowErr{line, fmt.Sprintf("email '%s' is already registered as a faculty member", *email)})
						continue
					}
				}
				// Create new volunteer
				err = tx.QueryRow(c.Context(), `
					INSERT INTO volunteers(name, email, phone, dept, college_id, role)
					VALUES ($1,$2,$3,$4,$5,$6)
					RETURNING id
				`, name, email, phone, dept, collegeID, models.UserRoleVolunteer).Scan(&vID)
				if err != nil {
					if strings.Contains(err.Error(), "volunteers_college_id_key") && collegeID != nil && *collegeID != "" {
						rowErrors = append(rowErrors, rowErr{line, fmt.Sprintf("Volunteer with college ID '%s' already exists.", *collegeID)})
					} else if strings.Contains(err.Error(), "volunteers_email_key") && email != nil && *email != "" {
						rowErrors = append(rowErrors, rowErr{line, fmt.Sprintf("Volunteer with email '%s' already exists.", *email)})
					} else {
						rowErrors = append(rowErrors, rowErr{line, fmt.Sprintf("insert volunteer: %v", err)})
					}
					continue
				}
				createdVols++
			}

			// Insert or update assignment
			var assignmentID int64
			var onConflictClause string
			if assignRole == "lead" { // Example: If role is lead, maybe update existing lead assignment or create new
				onConflictClause = `ON CONFLICT (event_id, committee_id, volunteer_id) DO UPDATE SET
					role = EXCLUDED.role,
					status = EXCLUDED.status,
					reporting_time = EXCLUDED.reporting_time,
					shift = EXCLUDED.shift,
					start_time = EXCLUDED.start_time,
					end_time = EXCLUDED.end_time,
					notes = EXCLUDED.notes
				`
			} else {
				// Default behavior, assumes unique constraint (event_id, committee_id, volunteer_id) handles updates
				onConflictClause = `ON CONFLICT (event_id, committee_id, volunteer_id) DO UPDATE SET
					role = EXCLUDED.role,
					status = EXCLUDED.status,
					reporting_time = EXCLUDED.reporting_time,
					shift = EXCLUDED.shift,
					start_time = EXCLUDED.start_time,
					end_time = EXCLUDED.end_time,
					notes = EXCLUDED.notes
				`
			}

			// Check if an existing assignment will be updated
			var existingAssignmentID sql.NullInt64
			_ = tx.QueryRow(c.Context(), `
				SELECT id FROM volunteer_assignments
				WHERE event_id = $1 AND committee_id = $2 AND volunteer_id = $3
			`, eventID, committeeID, vID).Scan(&existingAssignmentID)

			err = tx.QueryRow(c.Context(), `
				INSERT INTO volunteer_assignments(event_id, committee_id, volunteer_id, role, status, reporting_time, shift, start_time, end_time, notes)
				VALUES ($1,$2,$3,$4::assignment_role,$5::assignment_status,$6,$7,$8,$9,$10)
				`+onConflictClause+`
				RETURNING id
			`, eventID, committeeID, vID, assignRole, assignStatus, rt, shift, startTime, endTime, notes).Scan(&assignmentID)
			if err != nil {
				rowErrors = append(rowErrors, rowErr{line, fmt.Sprintf("insert/update assignment: %v", err)})
				continue
			}

			if existingAssignmentID.Valid {
				updatedAssigns++
			} else {
				createdAssigns++
			}
		}

		if err := tx.Commit(c.Context()); err != nil {
			return err
		}

		errs := make([]fiber.Map, 0, len(rowErrors))
		for _, e := range rowErrors {
			errs = append(errs, fiber.Map{"line": e.line, "error": e.msg})
		}

		return c.JSON(fiber.Map{
			"created_volunteers":  createdVols,
			"created_assignments": createdAssigns,
			"updated_assignments": updatedAssigns,
			"errors":              errs,
		})
	}
}

// ExportVolunteersCSV - GET /volunteers/export_csv (Admin)
// Exports all volunteer data to a CSV file.
func ExportVolunteersCSV(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		rows, err := pool.Query(c.Context(), `
			SELECT id, name, email, phone, dept, college_id, created_at
			FROM volunteers ORDER BY name
		`)
		if err != nil {
			return err
		}
		defer rows.Close()

		c.Set("Content-Type", "text/csv")
		c.Set("Content-Disposition", `attachment; filename="volunteers_export.csv"`)

		writer := csv.NewWriter(c.Response().BodyWriter())
		defer writer.Flush()

		// Write CSV header
		header := []string{"ID", "Name", "Email", "Phone", "Department", "College ID", "Created At"}
		if err := writer.Write(header); err != nil {
			log.Printf("Error writing CSV header: %v", err)
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to write CSV header")
		}

		for rows.Next() {
			var v models.Volunteer
			if err := rows.Scan(&v.ID, &v.Name, &v.Email, &v.Phone, &v.Dept, &v.CollegeID, &v.CreatedAt); err != nil {
				log.Printf("Error scanning volunteer row for export: %v", err)
				continue
			}

			record := []string{
				strconv.FormatInt(v.ID, 10),
				v.Name,
				derefString(v.Email),
				derefString(v.Phone),
				derefString(v.Dept),
				derefString(v.CollegeID),
				v.CreatedAt.Format(time.RFC3339),
			}
			if err := writer.Write(record); err != nil {
				log.Printf("Error writing CSV record for volunteer ID %d: %v", v.ID, err)
			}
		}

		if err := rows.Err(); err != nil {
			log.Printf("Error iterating volunteer rows for export: %v", err)
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to retrieve all volunteers for export")
		}

		return nil
	}
}

// ExportAssignmentsCSV - GET /volunteers/assignments/export_csv (Admin)
// Exports all volunteer assignments data to a CSV file.
func ExportAssignmentsCSV(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		rows, err := pool.Query(c.Context(), `
			SELECT
				va.id, va.event_id, va.committee_id, va.volunteer_id,
				va.role::text, va.status::text, va.reporting_time, va.shift, va.start_time, va.end_time, va.notes, va.created_at,
				v.name AS volunteer_name, v.email AS volunteer_email, v.college_id AS volunteer_college_id, -- NEW
				c.name AS committee_name,
				e.name AS event_name
			FROM volunteer_assignments va
			JOIN volunteers v ON v.id = va.volunteer_id
			JOIN committees c ON c.id = va.committee_id
			JOIN events e ON e.id = va.event_id
			ORDER BY e.name, c.name, v.name
		`)
		if err != nil {
			return err
		}
		defer rows.Close()

		c.Set("Content-Type", "text/csv")
		c.Set("Content-Disposition", `attachment; filename="volunteer_assignments_export.csv"`)

		writer := csv.NewWriter(c.Response().BodyWriter())
		defer writer.Flush()

		// Write CSV header
		header := []string{
			"Assignment ID", "Event ID", "Event Name", "Committee ID", "Committee Name",
			"Volunteer ID", "Volunteer Name", "Volunteer Email", "Volunteer College ID", // NEW
			"Role", "Status", "Reporting Time (ISO)", "Shift", "Start Time (ISO)", "End Time (ISO)", "Notes", "Created At (ISO)",
		}
		if err := writer.Write(header); err != nil {
			log.Printf("Error writing CSV header: %v", err)
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to write CSV header")
		}

		for rows.Next() {
			var a models.VolunteerAssignment
			var roleStr, statusStr string
			var volunteerEmail, volunteerCollegeID sql.NullString // NEW: For scanning college_id
			if err := rows.Scan(
				&a.ID, &a.EventID, &a.CommitteeID, &a.VolunteerID,
				&roleStr, &statusStr, &a.ReportingTime, &a.Shift, &a.StartTime, &a.EndTime, &a.Notes, &a.CreatedAt,
				&a.VolunteerName, &volunteerEmail, &volunteerCollegeID, // NEW: Scan into volunteerCollegeID
				&a.CommitteeName, &a.EventName,
			); err != nil {
				log.Printf("Error scanning assignment row for export: %v", err)
				continue
			}
			a.Role = models.AssignmentRole(roleStr)
			a.Status = models.AssignmentStatus(statusStr)
			a.VolunteerEmail = derefNullString(volunteerEmail)         // Assign dereferenced email
			a.VolunteerCollegeID = derefNullString(volunteerCollegeID) // NEW: Assign dereferenced college ID

			record := []string{
				strconv.FormatInt(a.ID, 10),
				strconv.FormatInt(a.EventID, 10),
				a.EventName,
				strconv.FormatInt(a.CommitteeID, 10),
				a.CommitteeName,
				strconv.FormatInt(a.VolunteerID, 10),
				a.VolunteerName,
				derefString(a.VolunteerEmail),
				derefString(a.VolunteerCollegeID), // NEW: Output college ID
				string(a.Role),
				string(a.Status),
				formatTimePtr(a.ReportingTime),
				derefString(a.Shift),
				formatTimePtr(a.StartTime),
				formatTimePtr(a.EndTime),
				derefString(a.Notes),
				a.CreatedAt.Format(time.RFC3339),
			}
			if err := writer.Write(record); err != nil {
				log.Printf("Error writing CSV record for assignment ID %d: %v", a.ID, err)
			}
		}

		if err := rows.Err(); err != nil {
			log.Printf("Error iterating assignment rows for export: %v", err)
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to retrieve all assignments for export")
		}

		return nil
	}
}

// --- Admin-Only Assignment CRUD ---

// CreateAssignment - POST /volunteers/assignments (Admin)
// Creates a specific assignment for an existing volunteer.
func CreateAssignment(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var b models.CreateVolunteerAssignmentRequest
		if err := c.BodyParser(&b); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Bad JSON")
		}
		if b.EventID <= 0 || b.CommitteeID <= 0 || b.VolunteerID <= 0 {
			return fiber.NewError(fiber.StatusBadRequest, "Event ID, Committee ID, and Volunteer ID are required")
		}

		role := normAssignmentRole(string(b.Role))
		status := normAssignmentStatus(string(b.Status))

		var assignment models.VolunteerAssignment
		var roleStr, statusStr string
		var volunteerEmail, volunteerCollegeID sql.NullString // NEW: For enriched fields
		// The RETURNING clause needs to match the structure of the SELECT below for enriched fields
		err := pool.QueryRow(c.Context(), `
			INSERT INTO volunteer_assignments(event_id, committee_id, volunteer_id, role, status, reporting_time, shift, start_time, end_time, notes)
			VALUES ($1,$2,$3,$4::assignment_role,$5::assignment_status,$6,$7,$8,$9,$10)
			ON CONFLICT (event_id, committee_id, volunteer_id) DO UPDATE SET
				role = EXCLUDED.role,
				status = EXCLUDED.status,
				reporting_time = EXCLUDED.reporting_time,
				shift = EXCLUDED.shift,
				start_time = EXCLUDED.start_time,
				end_time = EXCLUDED.end_time,
				notes = EXCLUDED.notes
			RETURNING id, event_id, committee_id, volunteer_id, role::text, status::text, 
				reporting_time, shift, start_time, end_time, notes, created_at
		`, b.EventID, b.CommitteeID, b.VolunteerID, role, status, b.ReportingTime, b.Shift, b.StartTime, b.EndTime, b.Notes).
			Scan(&assignment.ID, &assignment.EventID, &assignment.CommitteeID, &assignment.VolunteerID,
				&roleStr, &statusStr, &assignment.ReportingTime, &assignment.Shift, &assignment.StartTime, &assignment.EndTime, &assignment.Notes, &assignment.CreatedAt)
		if err != nil {
			return err
		}
		assignment.Role = models.AssignmentRole(roleStr)
		assignment.Status = models.AssignmentStatus(statusStr)

		// Now fetch the enriched fields after the insert/update
		err = pool.QueryRow(c.Context(), `
			SELECT 
				v.name AS volunteer_name, v.email AS volunteer_email, v.college_id AS volunteer_college_id,
				c.name AS committee_name, e.name AS event_name
			FROM volunteer_assignments va
			JOIN volunteers v ON v.id = va.volunteer_id
			JOIN committees c ON c.id = va.committee_id
			JOIN events e ON e.id = va.event_id
			WHERE va.id = $1
		`, assignment.ID).Scan(
			&assignment.VolunteerName, &volunteerEmail, &volunteerCollegeID,
			&assignment.CommitteeName, &assignment.EventName,
		)
		if err != nil {
			// This would be an unexpected error if the assignment was just created/updated
			log.Printf("Error fetching enriched assignment fields: %v", err)
			// Decide how to handle this - either return error or proceed with partial data
		}
		assignment.VolunteerEmail = derefNullString(volunteerEmail)
		assignment.VolunteerCollegeID = derefNullString(volunteerCollegeID)

		return c.Status(fiber.StatusCreated).JSON(assignment)
	}
}

// ListAssignments - GET /volunteers/assignments?event_id=&committee_id=&volunteer_id=&shift=&start_date=YYYY-MM-DD&end_date=YYYY-MM-DD&limit=&offset= (Admin)
// Lists all assignments, with optional filters.
func ListAssignments(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		filters := buildAssignmentFilters(c) // New helper to build filters

		args := []any{}
		whereClauses := []string{}
		paramCounter := 1

		if filters.EventID.Valid {
			whereClauses = append(whereClauses, "va.event_id=$"+itoa(paramCounter))
			args = append(args, filters.EventID.Int64)
			paramCounter++
		}
		if filters.CommitteeID.Valid {
			whereClauses = append(whereClauses, "va.committee_id=$"+itoa(paramCounter))
			args = append(args, filters.CommitteeID.Int64)
			paramCounter++
		}
		if filters.VolunteerID.Valid {
			whereClauses = append(whereClauses, "va.volunteer_id=$"+itoa(paramCounter))
			args = append(args, filters.VolunteerID.Int64)
			paramCounter++
		}
		if filters.Shift.Valid {
			whereClauses = append(whereClauses, "va.shift ILIKE $"+itoa(paramCounter))
			args = append(args, "%"+filters.Shift.String+"%")
			paramCounter++
		}
		if filters.StartDate.Valid {
			whereClauses = append(whereClauses, "DATE(va.start_time) >= $"+itoa(paramCounter))
			args = append(args, filters.StartDate.Time)
			paramCounter++
		}
		if filters.EndDate.Valid {
			whereClauses = append(whereClauses, "DATE(va.start_time) <= $"+itoa(paramCounter))
			args = append(args, filters.EndDate.Time)
			paramCounter++
		}

		where := ""
		if len(whereClauses) > 0 {
			where = "WHERE " + strings.Join(whereClauses, " AND ")
		}

		query := `
			SELECT
				va.id, va.event_id, va.committee_id, va.volunteer_id,
				va.role::text, va.status::text, va.reporting_time, va.shift, va.start_time, va.end_time, va.notes, va.created_at,
				v.name AS volunteer_name, v.email AS volunteer_email, v.college_id AS volunteer_college_id, -- NEW
				c.name AS committee_name,
				e.name AS event_name
			FROM volunteer_assignments va
			JOIN volunteers v ON v.id = va.volunteer_id
			JOIN committees c ON c.id = va.committee_id
			JOIN events e ON e.id = va.event_id
			` + where + `
			ORDER BY va.start_time DESC, va.created_at DESC
			LIMIT $` + itoa(paramCounter) + ` OFFSET $` + itoa(paramCounter+1)
		args = append(args, filters.Limit, filters.Offset)

		rows, err := pool.Query(c.Context(), query, args...)
		if err != nil {
			log.Printf("Error querying all assignments: %v", err)
			return err
		}
		defer rows.Close()

		out := []models.VolunteerAssignment{}
		for rows.Next() {
			var a models.VolunteerAssignment
			var roleStr, statusStr string
			var volunteerEmail, volunteerCollegeID sql.NullString // NEW
			if err := rows.Scan(
				&a.ID, &a.EventID, &a.CommitteeID, &a.VolunteerID,
				&roleStr, &statusStr, &a.ReportingTime, &a.Shift, &a.StartTime, &a.EndTime, &a.Notes, &a.CreatedAt,
				&a.VolunteerName, &volunteerEmail, &volunteerCollegeID, &a.CommitteeName, &a.EventName, // NEW
			); err != nil {
				log.Printf("Error scanning assignment row: %v", err)
				return err
			}
			a.Role = models.AssignmentRole(roleStr)
			a.Status = models.AssignmentStatus(statusStr)
			a.VolunteerEmail = derefNullString(volunteerEmail)         // NEW
			a.VolunteerCollegeID = derefNullString(volunteerCollegeID) // NEW
			out = append(out, a)
		}
		if err := rows.Err(); err != nil {
			log.Printf("Error iterating all assignments rows: %v", err)
			return err
		}
		return c.JSON(out)
	}
}

// GetAssignmentByID - GET /volunteers/assignments/:id (Admin)
func GetAssignmentByID(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		id, err := strconv.ParseInt(c.Params("id"), 10, 64)
		if err != nil || id <= 0 {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid assignment ID")
		}

		var a models.VolunteerAssignment
		var roleStr, statusStr string
		var volunteerEmail, volunteerCollegeID sql.NullString // NEW
		err = pool.QueryRow(c.Context(), `
			SELECT
				va.id, va.event_id, va.committee_id, va.volunteer_id,
				va.role::text, va.status::text, va.reporting_time, va.shift, va.start_time, va.end_time, va.notes, va.created_at,
				v.name AS volunteer_name, v.email AS volunteer_email, v.college_id AS volunteer_college_id, -- NEW
				c.name AS committee_name,
				e.name AS event_name
			FROM volunteer_assignments va
			JOIN volunteers v ON v.id = va.volunteer_id
			JOIN committees c ON c.id = va.committee_id
			JOIN events e ON e.id = va.event_id
			WHERE va.id = $1
		`, id).Scan(
			&a.ID, &a.EventID, &a.CommitteeID, &a.VolunteerID,
			&roleStr, &statusStr, &a.ReportingTime, &a.Shift, &a.StartTime, &a.EndTime, &a.Notes, &a.CreatedAt,
			&a.VolunteerName, &volunteerEmail, &volunteerCollegeID, &a.CommitteeName, &a.EventName, // NEW
		)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fiber.NewError(fiber.StatusNotFound, "Assignment not found")
			}
			return err
		}
		a.Role = models.AssignmentRole(roleStr)
		a.Status = models.AssignmentStatus(statusStr)
		a.VolunteerEmail = derefNullString(volunteerEmail)         // NEW
		a.VolunteerCollegeID = derefNullString(volunteerCollegeID) // NEW
		return c.JSON(a)
	}
}

// UpdateAssignment - PUT /volunteers/assignments/:id (Admin)
func UpdateAssignment(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		id, err := strconv.ParseInt(c.Params("id"), 10, 64)
		if err != nil || id <= 0 {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid assignment ID")
		}

		var b models.UpdateVolunteerAssignmentRequest
		if err := c.BodyParser(&b); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Bad JSON")
		}

		sets := []string{}
		args := []any{}
		i := 1

		if b.Role != nil {
			sets = append(sets, "role=$"+itoa(i)+`::assignment_role`)
			args = append(args, normAssignmentRole(string(*b.Role)))
			i++
		}
		if b.Status != nil {
			sets = append(sets, "status=$"+itoa(i)+`::assignment_status`)
			args = append(args, normAssignmentStatus(string(*b.Status)))
			i++
		}
		if b.ReportingTime != nil {
			sets = append(sets, "reporting_time=$"+itoa(i))
			args = append(args, *b.ReportingTime)
			i++
		}
		if b.Shift != nil {
			sets = append(sets, "shift=$"+itoa(i))
			args = append(args, nullable(strings.TrimSpace(*b.Shift)))
			i++
		}
		if b.StartTime != nil {
			sets = append(sets, "start_time=$"+itoa(i))
			args = append(args, *b.StartTime)
			i++
		}
		if b.EndTime != nil {
			sets = append(sets, "end_time=$"+itoa(i))
			args = append(args, *b.EndTime)
			i++
		}
		if b.Notes != nil {
			sets = append(sets, "notes=$"+itoa(i))
			args = append(args, nullable(strings.TrimSpace(*b.Notes)))
			i++
		}

		if len(sets) == 0 {
			return fiber.NewError(fiber.StatusBadRequest, "No fields to update")
		}
		args = append(args, id)

		sqlQuery := `UPDATE volunteer_assignments SET ` + strings.Join(sets, ", ") + ` WHERE id=$` + itoa(i)
		cmd, err := pool.Exec(c.Context(), sqlQuery, args...)
		if err != nil {
			return err
		}
		if cmd.RowsAffected() == 0 {
			return fiber.NewError(fiber.StatusNotFound, "Assignment not found")
		}
		return c.SendStatus(fiber.StatusNoContent)
	}
}

// DeleteAssignment - DELETE /volunteers/assignments/:id (Admin)
func DeleteAssignment(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		id, err := strconv.ParseInt(c.Params("id"), 10, 64)
		if err != nil || id <= 0 {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid assignment ID")
		}
		cmd, err := pool.Exec(c.Context(), `DELETE FROM volunteer_assignments WHERE id=$1`, id)
		if err != nil {
			return err
		}
		if cmd.RowsAffected() == 0 {
			return fiber.NewError(fiber.StatusNotFound, "Assignment not found")
		}
		return c.SendStatus(fiber.StatusNoContent)
	}
}

// --- Volunteer (Student) Specific Routes ---

// GetMyProfile - GET /volunteers/me (Volunteer)
func GetMyProfile(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		volunteerID, err := mw.GetUserIDFromClaims(c)
		if err != nil {
			return fiber.NewError(fiber.StatusUnauthorized, "Volunteer ID not found in token")
		}

		var v models.Volunteer
		err = pool.QueryRow(c.Context(), `
			SELECT id, name, email, phone, dept, college_id, created_at
			FROM volunteers WHERE id = $1
		`, volunteerID).Scan(&v.ID, &v.Name, &v.Email, &v.Phone, &v.Dept, &v.CollegeID, &v.CreatedAt)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fiber.NewError(fiber.StatusNotFound, "Your volunteer profile not found")
			}
			return err
		}
		return c.JSON(v)
	}
}

// SetMyPassword - POST /volunteers/me/set-password (Volunteer)
func SetMyPassword(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		volunteerID, err := mw.GetUserIDFromClaims(c)
		if err != nil {
			return fiber.NewError(fiber.StatusUnauthorized, "Volunteer ID not found in token")
		}

		var b models.SetVolunteerPasswordRequest
		if err := c.BodyParser(&b); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Bad JSON")
		}
		if len(b.NewPassword) < 8 {
			return fiber.NewError(fiber.StatusBadRequest, "New password must be at least 8 characters long")
		}

		var currentPasswordHash sql.NullString
		err = pool.QueryRow(c.Context(), `SELECT password_hash FROM volunteers WHERE id = $1`, volunteerID).Scan(&currentPasswordHash)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fiber.NewError(fiber.StatusNotFound, "Volunteer not found")
			}
			return err
		}

		if currentPasswordHash.Valid {
			if b.OldPassword == nil || *b.OldPassword == "" {
				return fiber.NewError(fiber.StatusBadRequest, "Old password is required to change your password")
			}
			if !hAuth.BcryptVerify(currentPasswordHash.String, *b.OldPassword) {
				return fiber.NewError(fiber.StatusUnauthorized, "Invalid old password")
			}
		} else {
			if b.OldPassword != nil && *b.OldPassword != "" {
				return fiber.NewError(fiber.StatusBadRequest, "No old password is set for your account, do not provide one.")
			}
		}

		newHash, err := hAuth.BcryptHash(b.NewPassword)
		if err != nil {
			return err
		}

		cmd, err := pool.Exec(c.Context(), `UPDATE volunteers SET password_hash = $1 WHERE id = $2`, newHash, volunteerID)
		if err != nil {
			return err
		}
		if cmd.RowsAffected() == 0 {
			return fiber.NewError(fiber.StatusNotFound, "Volunteer not found")
		}
		return c.JSON(fiber.Map{"message": "Password updated successfully"})
	}
}

// GetMyAssignments - GET /volunteers/me/assignments (Volunteer)
// Lists all assignments for the logged-in volunteer.
func GetMyAssignments(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		volunteerID, err := mw.GetUserIDFromClaims(c)
		if err != nil {
			return fiber.NewError(fiber.StatusUnauthorized, "Volunteer ID not found in token")
		}

		limit := clampInt(c.QueryInt("limit", 100), 1, 500)
		offset := maxInt(c.QueryInt("offset", 0), 0)

		rows, err := pool.Query(c.Context(), `
			SELECT
				va.id, va.event_id, va.committee_id, va.volunteer_id,
				va.role::text, va.status::text, va.reporting_time, va.shift, va.start_time, va.end_time, va.notes, va.created_at,
				v.name AS volunteer_name, v.email AS volunteer_email, v.college_id AS volunteer_college_id, -- NEW
				c.name AS committee_name,
				e.name AS event_name,
				-- Check for active attendance today for this assignment
				(SELECT att.id FROM attendance att WHERE att.assignment_id = va.id AND DATE(att.check_in_time) = CURRENT_DATE AND att.check_out_time IS NULL LIMIT 1) AS active_attendance_id
			FROM volunteer_assignments va
			JOIN volunteers v ON v.id = va.volunteer_id
			JOIN committees c ON c.id = va.committee_id
			JOIN events e ON e.id = va.event_id
			WHERE va.volunteer_id = $1
			ORDER BY va.created_at DESC
			LIMIT $2 OFFSET $3
		`, volunteerID, limit, offset)
		if err != nil {
			return err
		}
		defer rows.Close()

		type MyAssignment struct { // Extend the base model for specific view
			models.VolunteerAssignment
			ActiveAttendanceID sql.NullInt64 `json:"active_attendance_id,omitempty"`
			IsCheckedInToday   bool          `json:"is_checked_in_today"`
		}
		out := []MyAssignment{}
		for rows.Next() {
			var a MyAssignment
			var roleStr, statusStr string
			var activeAttendanceID sql.NullInt64
			var volunteerEmail, volunteerCollegeID sql.NullString // NEW
			if err := rows.Scan(
				&a.ID, &a.EventID, &a.CommitteeID, &a.VolunteerID,
				&roleStr, &statusStr, &a.ReportingTime, &a.Shift, &a.StartTime, &a.EndTime, &a.Notes, &a.CreatedAt,
				&a.VolunteerName, &volunteerEmail, &volunteerCollegeID, &a.CommitteeName, &a.EventName, // NEW
				&activeAttendanceID,
			); err != nil {
				return err
			}
			a.Role = models.AssignmentRole(roleStr)
			a.Status = models.AssignmentStatus(statusStr)
			a.VolunteerEmail = derefNullString(volunteerEmail)         // NEW
			a.VolunteerCollegeID = derefNullString(volunteerCollegeID) // NEW
			a.ActiveAttendanceID = activeAttendanceID
			a.IsCheckedInToday = activeAttendanceID.Valid // If ID is valid, they are checked in today
			out = append(out, a)
		}
		return c.JSON(out)
	}
}

// GetMyCommittees - GET /volunteers/me/committees (Volunteer)
// Lists all committees the logged-in volunteer is assigned to.
func GetMyCommittees(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		volunteerID, err := mw.GetUserIDFromClaims(c)
		if err != nil {
			return fiber.NewError(fiber.StatusUnauthorized, "Volunteer ID not found in token")
		}

		limit := clampInt(c.QueryInt("limit", 100), 1, 500)
		offset := maxInt(c.QueryInt("offset", 0), 0)

		rows, err := pool.Query(c.Context(), `
			SELECT DISTINCT
				c.id, c.event_id, c.name, COALESCE(c.description,''), c.created_at, e.name as event_name
			FROM committees c
			JOIN volunteer_assignments va ON va.committee_id = c.id
			JOIN events e ON e.id = c.event_id
			WHERE va.volunteer_id = $1
			ORDER BY c.name
			LIMIT $2 OFFSET $3
		`, volunteerID, limit, offset)
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

// assignmentFilters struct for building dynamic queries
type assignmentFilters struct {
	EventID     sql.NullInt64
	CommitteeID sql.NullInt64
	VolunteerID sql.NullInt64
	Shift       sql.NullString
	StartDate   sql.NullTime
	EndDate     sql.NullTime
	Limit       int
	Offset      int
}

// buildAssignmentFilters parses query parameters into an assignmentFilters struct
func buildAssignmentFilters(c *fiber.Ctx) assignmentFilters {
	filters := assignmentFilters{}

	eventIDStr := c.Query("event_id", "")
	if eventIDStr != "" {
		if id, err := strconv.ParseInt(eventIDStr, 10, 64); err == nil {
			filters.EventID = sql.NullInt64{Int64: id, Valid: true}
		}
	}

	committeeIDStr := c.Query("committee_id", "")
	if committeeIDStr != "" {
		if id, err := strconv.ParseInt(committeeIDStr, 10, 64); err == nil {
			filters.CommitteeID = sql.NullInt64{Int64: id, Valid: true}
		}
	}

	volunteerIDStr := c.Query("volunteer_id", "")
	if volunteerIDStr != "" {
		if id, err := strconv.ParseInt(volunteerIDStr, 10, 64); err == nil {
			filters.VolunteerID = sql.NullInt64{Int64: id, Valid: true}
		}
	}

	shiftStr := c.Query("shift", "")
	if shiftStr != "" {
		filters.Shift = sql.NullString{String: shiftStr, Valid: true}
	}

	startDateStr := c.Query("start_date", "")
	if startDateStr != "" {
		if t, err := time.Parse("2006-01-02", startDateStr); err == nil {
			filters.StartDate = sql.NullTime{Time: t, Valid: true}
		}
	}

	endDateStr := c.Query("end_date", "")
	if endDateStr != "" {
		if t, err := time.Parse("2006-01-02", endDateStr); err == nil {
			filters.EndDate = sql.NullTime{Time: t, Valid: true}
		}
	}

	filters.Limit = clampInt(c.QueryInt("limit", 100), 1, 500)
	filters.Offset = maxInt(c.QueryInt("offset", 0), 0)

	return filters
}

// --- Helpers ---

func indexer(header []string) map[string]int {
	m := map[string]int{}
	for i, h := range header {
		m[strings.ToLower(strings.TrimSpace(h))] = i
	}
	return m
}
func get(rec []string, idx map[string]int, key string) string {
	i, ok := idx[key]
	if !ok || i < 0 || i >= len(rec) {
		return ""
	}
	return rec[i]
}
func trim(s string) string { return strings.TrimSpace(s) }
func defaultIfEmpty(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// derefNullString is a helper to convert sql.NullString to *string.
// Useful for populating models.VolunteerAssignment.VolunteerEmail and .VolunteerCollegeID.
func derefNullString(s sql.NullString) *string {
	if s.Valid {
		return &s.String
	}
	return nil
}

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

func normAssignmentRole(r string) models.AssignmentRole {
	switch strings.ToLower(strings.TrimSpace(r)) {
	case "lead":
		return models.RoleLead
	case "support":
		return models.RoleSupport
	default:
		return models.RoleVolunteer // Default
	}
}

func normAssignmentStatus(s string) models.AssignmentStatus {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "standby":
		return models.StatusStandby
	case "cancelled":
		return models.StatusCancelled
	default:
		return models.StatusAssigned // Default
	}
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func formatTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(time.RFC3339)
}
