package attendance

import (
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"log" // Added for logging errors in CSV export
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	mw "Seva-app-backend/middleware"
	"Seva-app-backend/models"
)

// Register mounts attendance routes under /attendance
func Register(g fiber.Router, pool *pgxpool.Pool, jwtGuard fiber.Handler, requireFaculty fiber.Handler, requireVolunteer fiber.Handler) {
	// Volunteer actions
	g.Post("/checkin", jwtGuard, requireVolunteer, CheckIn(pool))
	g.Post("/checkout", jwtGuard, requireVolunteer, CheckOut(pool))

	// Faculty/Admin actions (no approval needed)
	g.Get("/shifts-without-checkin", jwtGuard, requireFaculty, ListShiftsWithoutCheckIn(pool))
	g.Get("/active-in-shift", jwtGuard, requireFaculty, ListActiveCheckinsInShift(pool))         // NEW
	g.Get("/active-in-committee", jwtGuard, requireFaculty, ListActiveCheckinsInCommittee(pool)) // NEW
	g.Post("/checkout-shift", jwtGuard, requireFaculty, CheckoutShift(pool))                     // NEW

	g.Get("/assignments-status", jwtGuard, requireFaculty, ListAssignmentsWithCheckinStatus(pool)) // <--- NEW ROUTE
	// General attendance list and export for Faculty/Admin
	g.Get("/", jwtGuard, requireFaculty, ListAllAttendance(pool))
	g.Get("/export_csv", jwtGuard, requireFaculty, ExportAttendanceCSV(pool))
}

// POST /attendance/checkin  {assignment_id, lat?, lng?, time?}
// A volunteer can only check-in for their own assignments.
func CheckIn(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		_, err := mw.GetUserIDFromClaims(c)
		if err != nil {
			return fiber.NewError(fiber.StatusUnauthorized, "Volunteer ID not found in token")
		}

		var b models.CheckInRequest
		if err := c.BodyParser(&b); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Bad JSON")
		}
		if b.AssignmentID <= 0 {
			return fiber.NewError(fiber.StatusBadRequest, "assignment_id is required")
		}

		// Parse time
		ts := time.Now()
		if b.TimeISO != nil && *b.TimeISO != "" {
			t, err := time.Parse(time.RFC3339, *b.TimeISO)
			if err != nil {
				return fiber.NewError(fiber.StatusBadRequest, "Bad time (RFC3339)")
			}
			ts = t
		}

		// Ensure the assignment exists AND belongs to the logged-in volunteer
		// Ensure the assignment exists
		var assignmentExists bool
		if err := pool.QueryRow(c.Context(),
			`SELECT EXISTS(SELECT 1 FROM volunteer_assignments WHERE id=$1)`, b.AssignmentID).Scan(&assignmentExists); err != nil {
			return err
		}
		if !assignmentExists {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid assignment_id")
		}

		// Prevent duplicate check-ins for the same assignment on the same day without checking out.
		var existingAttendanceID int64
		err = pool.QueryRow(c.Context(),
			`SELECT id FROM attendance WHERE assignment_id=$1 AND check_out_time IS NULL AND DATE(check_in_time) = DATE($2)`,
			b.AssignmentID, ts).Scan(&existingAttendanceID)
		if err == nil {
			return fiber.NewError(fiber.StatusConflict, "Already checked in for this assignment and not checked out.")
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err // Actual DB error
		}

		var newAttendanceID int64
		err = pool.QueryRow(c.Context(),
			`INSERT INTO attendance(assignment_id, check_in_time, lat, lng)
			 VALUES ($1,$2,$3,$4) RETURNING id`,
			b.AssignmentID, ts, b.Lat, b.Lng).Scan(&newAttendanceID)
		if err != nil {
			return err
		}
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "checked_in", "attendance_id": newAttendanceID})
	}
}

// POST /attendance/checkout  {attendance_id, time?}
// A volunteer can only check-out for their own attendance records.
func CheckOut(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		_, err := mw.GetUserIDFromClaims(c)
		if err != nil {
			return fiber.NewError(fiber.StatusUnauthorized, "Volunteer ID not found in token")
		}

		var b models.CheckOutRequest
		if err := c.BodyParser(&b); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Bad JSON")
		}
		if b.AttendanceID <= 0 {
			return fiber.NewError(fiber.StatusBadRequest, "attendance_id is required")
		}
		ts := time.Now()
		if b.TimeISO != nil && *b.TimeISO != "" {
			t, err := time.Parse(time.RFC3339, *b.TimeISO)
			if err != nil {
				return fiber.NewError(fiber.StatusBadRequest, "Bad time (RFC3339)")
			}
			ts = t
		}

		// Ensure the attendance record exists AND belongs to the logged-in volunteer AND is currently active (check_out_time IS NULL)
		// Ensure the attendance record exists and is currently active (check_out_time IS NULL)
		var attendanceExists bool
		err = pool.QueryRow(c.Context(),
			`SELECT EXISTS(SELECT 1 FROM attendance WHERE id = $1 AND check_out_time IS NULL)`,
			b.AttendanceID).Scan(&attendanceExists)
		if err != nil {
			return err
		}
		if !attendanceExists {
			// Check if it exists but is already checked out
			var checkOutTime sql.NullTime
			_ = pool.QueryRow(c.Context(), `SELECT check_out_time FROM attendance WHERE id=$1`, b.AttendanceID).Scan(&checkOutTime)
			if checkOutTime.Valid {
				return fiber.NewError(fiber.StatusConflict, "Already checked out")
			}
			return fiber.NewError(fiber.StatusNotFound, "Active attendance record not found")
		}

		cmd, err := pool.Exec(c.Context(),
			`UPDATE attendance SET check_out_time=$2 WHERE id=$1 AND check_out_time IS NULL`,
			b.AttendanceID, ts)
		if err != nil {
			return err
		}
		if cmd.RowsAffected() == 0 {
			return fiber.NewError(fiber.StatusNotFound, "Attendance not found or already checked out")
		}
		return c.SendStatus(fiber.StatusNoContent)
	}
}

// ListShiftsWithoutCheckIn - GET /attendance/shifts-without-checkin?event_id=&committee_id=&shift=&date=YYYY-MM-DD&limit=100&offset=0
// For Faculty/Admin to view volunteer assignments that have a start_time on a specific date but no check-in record for that day.
func ListShiftsWithoutCheckIn(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		filters := buildShiftCheckinFilters(c) // Use common filter builder for shifts

		args := []any{}
		whereConditions := []string{"TRUE"} // Start with TRUE to easily append AND conditions
		paramCounter := 1

		if filters.EventID.Valid {
			whereConditions = append(whereConditions, "va.event_id=$"+strconv.Itoa(paramCounter))
			args = append(args, filters.EventID.Int64)
			paramCounter++
		}
		if filters.CommitteeID.Valid {
			whereConditions = append(whereConditions, "va.committee_id=$"+strconv.Itoa(paramCounter))
			args = append(args, filters.CommitteeID.Int64)
			paramCounter++
		}
		if filters.Shift.Valid {
			whereConditions = append(whereConditions, "va.shift ILIKE $"+strconv.Itoa(paramCounter))
			args = append(args, "%"+filters.Shift.String+"%") // Case-insensitive search
			paramCounter++
		}

		// Filter for assignments whose start_time falls on the targetDate
		// Also, ensure there is NO attendance record for this assignment on this specific day.
		whereConditions = append(whereConditions, "DATE(va.start_time) = $"+strconv.Itoa(paramCounter))
		args = append(args, filters.Date.Time)
		paramCounter++

		// Subquery to find assignments that *do* have a check-in for the targetDate
		// Then exclude them from the main query.
		whereConditions = append(whereConditions, `
			va.id NOT IN (
				SELECT DISTINCT assignment_id
				FROM attendance
				WHERE DATE(check_in_time) = $`+strconv.Itoa(paramCounter)+`
			)
		`)
		args = append(args, filters.Date.Time) // Use targetDate again for the subquery
		paramCounter++

		whereClause := "WHERE " + strings.Join(whereConditions, " AND ")

		// Add limit/offset
		args = append(args, filters.Limit, filters.Offset)
		query := `
		  SELECT
		    va.id AS assignment_id,
		    va.event_id,
		    va.committee_id,
		    va.volunteer_id,
		    v.name AS volunteer_name,
		    v.dept AS volunteer_dept,
			v.college_id AS volunteer_college_id, -- NEW
		    c.name AS committee_name,
		    e.name AS event_name,
			va.role::text AS assignment_role_text,
			va.status::text AS assignment_status_text,
			va.reporting_time,
			va.start_time,
			va.end_time,
			va.shift,
			va.notes
		  FROM
		    volunteer_assignments va
		  JOIN
		    volunteers v ON v.id = va.volunteer_id
		  JOIN
		    committees c ON c.id = va.committee_id
		  JOIN
		    events e ON e.id = va.event_id
		  ` + whereClause + `
		  ORDER BY va.event_id, va.committee_id, va.start_time, v.name ASC
		  LIMIT $` + strconv.Itoa(paramCounter) + ` OFFSET $` + strconv.Itoa(paramCounter+1)

		rows, err := pool.Query(c.Context(), query, args...)
		if err != nil {
			log.Printf("Error querying shifts without check-in: %v", err)
			return err
		}
		defer rows.Close()

		out := make([]models.PendingShiftRow, 0, filters.Limit)
		for rows.Next() {
			var r models.PendingShiftRow
			var volunteerDept, volunteerCollegeID sql.NullString // NEW: collegeID
			var reportingTime, startTime, endTime sql.NullTime
			var shift, notes, assignmentRoleStr, assignmentStatusStr sql.NullString

			err := rows.Scan(
				&r.AssignmentID, &r.EventID, &r.CommitteeID, &r.VolunteerID,
				&r.VolunteerName, &volunteerDept, &volunteerCollegeID, &r.CommitteeName, &r.EventName, // NEW: Scan collegeID
				&assignmentRoleStr, &assignmentStatusStr, &reportingTime, &startTime, &endTime, &shift, &notes,
			)
			if err != nil {
				log.Printf("Error scanning pending shifts row: %v", err)
				return err
			}

			if volunteerDept.Valid {
				r.VolunteerDept = &volunteerDept.String
			}
			if volunteerCollegeID.Valid { // NEW
				r.VolunteerCollegeID = &volunteerCollegeID.String
			}
			if reportingTime.Valid {
				r.ReportingTime = &reportingTime.Time
			}
			if startTime.Valid {
				r.StartTime = &startTime.Time
			}
			if endTime.Valid {
				r.EndTime = &endTime.Time
			}
			if shift.Valid {
				r.Shift = &shift.String
			}
			if notes.Valid {
				r.Notes = &notes.String
			}
			r.AssignmentRole = models.AssignmentRole(assignmentRoleStr.String)
			r.AssignmentStatus = models.AssignmentStatus(assignmentStatusStr.String)

			out = append(out, r)

		}
		return c.JSON(out)
	}
}

// ListActiveCheckinsInShift - GET /attendance/active-in-shift?event_id=&committee_id=&shift=&date=YYYY-MM-DD
// Lists all volunteers currently checked in (check_out_time IS NULL) for a specific shift on a given day.
func ListActiveCheckinsInShift(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		filters := buildShiftCheckinFilters(c) // Re-use common filter builder

		args := []any{}
		whereConditions := []string{"a.check_out_time IS NULL"} // Only active check-ins
		paramCounter := 1

		if filters.EventID.Valid {
			whereConditions = append(whereConditions, "va.event_id=$"+strconv.Itoa(paramCounter))
			args = append(args, filters.EventID.Int64)
			paramCounter++
		}
		if filters.CommitteeID.Valid {
			whereConditions = append(whereConditions, "va.committee_id=$"+strconv.Itoa(paramCounter))
			args = append(args, filters.CommitteeID.Int64)
			paramCounter++
		}
		if filters.Shift.Valid {
			whereConditions = append(whereConditions, "va.shift ILIKE $"+strconv.Itoa(paramCounter))
			args = append(args, "%"+filters.Shift.String+"%")
			paramCounter++
		}

		// Filter by the date of check-in_time
		whereConditions = append(whereConditions, "DATE(a.check_in_time) = $"+strconv.Itoa(paramCounter))
		args = append(args, filters.Date.Time)
		paramCounter++

		whereClause := "WHERE " + strings.Join(whereConditions, " AND ")

		args = append(args, filters.Limit, filters.Offset) // Apply limit/offset
		query := `
		  SELECT
		    a.id, a.assignment_id, a.check_in_time, a.check_out_time, a.lat, a.lng,
			va.shift, -- NEW: Include shift from assignment
		    v.id AS volunteer_id, v.name AS volunteer_name, v.college_id AS volunteer_college_id, -- NEW
		    c.id AS committee_id, c.name AS committee_name,
		    e.id AS event_id, e.name AS event_name
		  FROM attendance a
		  JOIN volunteer_assignments va ON va.id = a.assignment_id
		  JOIN volunteers v ON v.id = va.volunteer_id
		  JOIN committees c ON c.id = va.committee_id
		  JOIN events e ON e.id = va.event_id
		  ` + whereClause + `
		  ORDER BY a.check_in_time DESC
		  LIMIT $` + strconv.Itoa(paramCounter) + ` OFFSET $` + strconv.Itoa(paramCounter+1)

		rows, err := pool.Query(c.Context(), query, args...)
		if err != nil {
			log.Printf("Error querying active check-ins in shift: %v", err)
			return err
		}
		defer rows.Close()

		out := make([]models.Attendance, 0, filters.Limit)
		for rows.Next() {
			var att models.Attendance
			var checkOutTime sql.NullTime
			var lat, lng sql.NullFloat64
			var shift sql.NullString
			var volunteerCollegeID sql.NullString // NEW

			err := rows.Scan(&att.ID, &att.AssignmentID, &att.CheckInTime, &checkOutTime, &lat, &lng,
				&shift,
				&att.VolunteerID, &att.VolunteerName, &volunteerCollegeID, // NEW
				&att.CommitteeID, &att.CommitteeName,
				&att.EventID, &att.EventName)
			if err != nil {
				log.Printf("Error scanning active check-ins row in shift: %v", err)
				return err
			}

			if checkOutTime.Valid {
				att.CheckOutTime = &checkOutTime.Time
			}
			if lat.Valid {
				att.Lat = &lat.Float64
			}
			if lng.Valid {
				att.Lng = &lng.Float64
			}
			if shift.Valid {
				att.Shift = &shift.String
			}
			if volunteerCollegeID.Valid { // NEW
				att.VolunteerCollegeID = &volunteerCollegeID.String
			}

			out = append(out, att)

		}
		return c.JSON(out)
	}
}

// ListActiveCheckinsInCommittee - GET /attendance/active-in-committee?event_id=&committee_id=
// Lists all volunteers currently checked in (check_out_time IS NULL) for any shift within a specific committee.
func ListActiveCheckinsInCommittee(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		eventIDFilter := sql.NullInt64{}
		eventIDStr := c.Query("event_id", "")
		if eventIDStr != "" {
			id, err := strconv.ParseInt(eventIDStr, 10, 64)
			if err != nil {
				return fiber.NewError(fiber.StatusBadRequest, "invalid event_id")
			}
			eventIDFilter = sql.NullInt64{Int64: id, Valid: true}
		}
		committeeIDFilter := sql.NullInt64{}
		committeeIDStr := c.Query("committee_id", "")
		if committeeIDStr != "" {
			id, err := strconv.ParseInt(committeeIDStr, 10, 64)
			if err != nil {
				return fiber.NewError(fiber.StatusBadRequest, "invalid committee_id")
			}
			committeeIDFilter = sql.NullInt64{Int64: id, Valid: true}
		} else {
			return fiber.NewError(fiber.StatusBadRequest, "committee_id is required for this endpoint")
		}

		limit := clampInt(c.QueryInt("limit", 100), 1, 500)
		offset := maxInt(c.QueryInt("offset", 0), 0)

		args := []any{}
		whereConditions := []string{"a.check_out_time IS NULL"} // Only active check-ins
		paramCounter := 1

		if eventIDFilter.Valid {
			whereConditions = append(whereConditions, "va.event_id=$"+strconv.Itoa(paramCounter))
			args = append(args, eventIDFilter.Int64)
			paramCounter++
		}
		if committeeIDFilter.Valid { // This is guaranteed valid by above check
			whereConditions = append(whereConditions, "va.committee_id=$"+strconv.Itoa(paramCounter))
			args = append(args, committeeIDFilter.Int64)
			paramCounter++
		}

		whereClause := "WHERE " + strings.Join(whereConditions, " AND ")

		args = append(args, limit, offset) // Apply limit/offset
		query := `
		  SELECT
		    a.id, a.assignment_id, a.check_in_time, a.check_out_time, a.lat, a.lng,
			va.shift, -- NEW: Include shift from assignment
		    v.id AS volunteer_id, v.name AS volunteer_name, v.college_id AS volunteer_college_id, -- NEW
		    c.id AS committee_id, c.name AS committee_name,
		    e.id AS event_id, e.name AS event_name
		  FROM attendance a
		  JOIN volunteer_assignments va ON va.id = a.assignment_id
		  JOIN volunteers v ON v.id = va.volunteer_id
		  JOIN committees c ON c.id = va.committee_id
		  JOIN events e ON e.id = va.event_id
		  ` + whereClause + `
		  ORDER BY a.check_in_time DESC
		  LIMIT $` + strconv.Itoa(paramCounter) + ` OFFSET $` + strconv.Itoa(paramCounter+1)

		rows, err := pool.Query(c.Context(), query, args...)
		if err != nil {
			log.Printf("Error querying active check-ins in committee: %v", err)
			return err
		}
		defer rows.Close()

		out := make([]models.Attendance, 0, limit)
		for rows.Next() {
			var att models.Attendance
			var checkOutTime sql.NullTime
			var lat, lng sql.NullFloat64
			var shift sql.NullString
			var volunteerCollegeID sql.NullString // NEW

			err := rows.Scan(&att.ID, &att.AssignmentID, &att.CheckInTime, &checkOutTime, &lat, &lng,
				&shift,
				&att.VolunteerID, &att.VolunteerName, &volunteerCollegeID, // NEW
				&att.CommitteeID, &att.CommitteeName,
				&att.EventID, &att.EventName)
			if err != nil {
				log.Printf("Error scanning active check-ins row in committee: %v", err)
				return err
			}

			if checkOutTime.Valid {
				att.CheckOutTime = &checkOutTime.Time
			}
			if lat.Valid {
				att.Lat = &lat.Float64
			}
			if lng.Valid {
				att.Lng = &lng.Float64
			}
			if shift.Valid {
				att.Shift = &shift.String
			}
			if volunteerCollegeID.Valid { // NEW
				att.VolunteerCollegeID = &volunteerCollegeID.String
			}

			out = append(out, att)

		}
		return c.JSON(out)
	}
}

// CheckoutShift - POST /attendance/checkout-shift?event_id=&committee_id=&shift=&date=YYYY-MM-DD
// Marks all active attendance records for a specific shift on a given day as checked out.
func CheckoutShift(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		filters := buildShiftCheckinFilters(c)

		if !filters.EventID.Valid || !filters.CommitteeID.Valid || !filters.Shift.Valid {
			return fiber.NewError(fiber.StatusBadRequest, "event_id, committee_id, and shift are required to checkout a shift")
		}

		// Ensure the current user is Faculty or Admin
		_, err := mw.GetUserIDFromClaims(c)
		if err != nil {
			return fiber.NewError(fiber.StatusUnauthorized, "Only authorized personnel can perform a shift checkout")
		}

		now := time.Now()

		// First, get all active attendance IDs that match the criteria
		activeQuery := `
            SELECT a.id
            FROM attendance a
            JOIN volunteer_assignments va ON va.id = a.assignment_id
            WHERE
                a.check_out_time IS NULL AND
                va.event_id = $1 AND
                va.committee_id = $2 AND
                va.shift ILIKE $3
        `
		activeArgs := []any{filters.EventID.Int64, filters.CommitteeID.Int64, "%" + filters.Shift.String + "%"}

		rows, err := pool.Query(c.Context(), activeQuery, activeArgs...)
		if err != nil {
			log.Printf("Error finding active attendance records: %v", err)
			return err
		}
		defer rows.Close()

		var attendanceIDs []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				log.Printf("Error scanning attendance ID: %v", err)
				continue
			}
			attendanceIDs = append(attendanceIDs, id)
		}

		log.Printf("Found %d active attendance records to checkout", len(attendanceIDs))

		if len(attendanceIDs) == 0 {
			return c.JSON(fiber.Map{"message": "No active attendances found for the specified shift."})
		}

		// Update each attendance record
		var checkedOut int64
		for _, id := range attendanceIDs {
			cmd, err := pool.Exec(c.Context(),
				`UPDATE attendance SET check_out_time = $1 WHERE id = $2 AND check_out_time IS NULL`,
				now, id)
			if err != nil {
				log.Printf("Error checking out attendance ID %d: %v", id, err)
				continue
			}
			checkedOut += cmd.RowsAffected()
		}

		return c.JSON(fiber.Map{"message": fmt.Sprintf("%d active attendances checked out for shift '%s'.", checkedOut, filters.Shift.String)})
	}
}

// ListAllAttendance - GET /attendance?event_id=&committee_id=&volunteer_id=&shift=&start_date=YYYY-MM-DD&end_date=YYYY-MM-DD&limit=100&offset=0
// For Faculty/Admin to view all attendance records with optional filters.
func ListAllAttendance(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		filters := buildAttendanceFilters(c)
		args := []any{}
		whereConditions := []string{}
		paramCounter := 1

		if filters.EventID.Valid {
			whereConditions = append(whereConditions, "va.event_id=$"+strconv.Itoa(paramCounter))
			args = append(args, filters.EventID.Int64)
			paramCounter++
		}
		if filters.CommitteeID.Valid {
			whereConditions = append(whereConditions, "va.committee_id=$"+strconv.Itoa(paramCounter))
			args = append(args, filters.CommitteeID.Int64)
			paramCounter++
		}
		if filters.VolunteerID.Valid {
			whereConditions = append(whereConditions, "va.volunteer_id=$"+strconv.Itoa(paramCounter))
			args = append(args, filters.VolunteerID.Int64)
			paramCounter++
		}
		if filters.Shift.Valid {
			whereConditions = append(whereConditions, "va.shift ILIKE $"+strconv.Itoa(paramCounter))
			args = append(args, "%"+filters.Shift.String+"%")
			paramCounter++
		}
		if filters.StartDate.Valid {
			whereConditions = append(whereConditions, "DATE(a.check_in_time) >= $"+strconv.Itoa(paramCounter))
			args = append(args, filters.StartDate.Time)
			paramCounter++
		}
		if filters.EndDate.Valid {
			whereConditions = append(whereConditions, "DATE(a.check_in_time) <= $"+strconv.Itoa(paramCounter))
			args = append(args, filters.EndDate.Time)
			paramCounter++
		}

		whereClause := ""
		if len(whereConditions) > 0 {
			whereClause = "WHERE " + strings.Join(whereConditions, " AND ")
		}

		args = append(args, filters.Limit, filters.Offset)
		query := `
		  SELECT a.id, a.assignment_id, a.check_in_time, a.check_out_time, a.lat, a.lng,
		         v.id AS volunteer_id, v.name AS volunteer_name, v.college_id AS volunteer_college_id, -- NEW
		         c.id AS committee_id, c.name AS committee_name,
		         e.id AS event_id, e.name AS event_name,
				 va.shift AS assignment_shift
		  FROM attendance a
		  JOIN volunteer_assignments va ON va.id = a.assignment_id
		  JOIN volunteers v ON v.id = va.volunteer_id
		  JOIN committees c ON c.id = va.committee_id
		  JOIN events e ON e.id = va.event_id
		  ` + whereClause + `
		  ORDER BY a.check_in_time DESC
		  LIMIT $` + strconv.Itoa(paramCounter) + ` OFFSET $` + strconv.Itoa(paramCounter+1)

		rows, err := pool.Query(c.Context(), query, args...)
		if err != nil {
			log.Printf("Error querying all attendance: %v", err)
			return err
		}
		defer rows.Close()

		out := make([]models.Attendance, 0, filters.Limit)
		for rows.Next() {
			var att models.Attendance
			var checkOutTime sql.NullTime
			var lat, lng sql.NullFloat64
			var assignmentShift sql.NullString
			var volunteerCollegeID sql.NullString // NEW

			err := rows.Scan(&att.ID, &att.AssignmentID, &att.CheckInTime, &checkOutTime, &lat, &lng,
				&att.VolunteerID, &att.VolunteerName, &volunteerCollegeID, // NEW
				&att.CommitteeID, &att.CommitteeName,
				&att.EventID, &att.EventName,
				&assignmentShift)
			if err != nil {
				log.Printf("Error scanning attendance row for ListAllAttendance: %v", err)
				return err
			}

			if checkOutTime.Valid {
				att.CheckOutTime = &checkOutTime.Time
			}
			if lat.Valid {
				att.Lat = &lat.Float64
			}
			if lng.Valid {
				att.Lng = &lng.Float64
			}
			if assignmentShift.Valid {
				att.Shift = &assignmentShift.String
			}
			if volunteerCollegeID.Valid { // NEW
				att.VolunteerCollegeID = &volunteerCollegeID.String
			}

			out = append(out, att)
		}
		if err := rows.Err(); err != nil {
			log.Printf("Error iterating all attendance rows: %v", err)
			return err
		}
		return c.JSON(out)
	}
}

// ExportAttendanceCSV - GET /attendance/export_csv?event_id=&committee_id=&volunteer_id=&shift=&start_date=YYYY-MM-DD&end_date=YYYY-MM-DD
// Exports attendance records to a CSV file.
func ExportAttendanceCSV(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		filters := buildAttendanceFilters(c) // Re-use filter building logic

		args := []any{}
		whereConditions := []string{}
		paramCounter := 1

		if filters.EventID.Valid {
			whereConditions = append(whereConditions, "va.event_id=$"+strconv.Itoa(paramCounter))
			args = append(args, filters.EventID.Int64)
			paramCounter++
		}
		if filters.CommitteeID.Valid {
			whereConditions = append(whereConditions, "va.committee_id=$"+strconv.Itoa(paramCounter))
			args = append(args, filters.CommitteeID.Int64)
			paramCounter++
		}
		if filters.VolunteerID.Valid {
			whereConditions = append(whereConditions, "va.volunteer_id=$"+strconv.Itoa(paramCounter))
			args = append(args, filters.VolunteerID.Int64)
			paramCounter++
		}
		if filters.Shift.Valid {
			whereConditions = append(whereConditions, "va.shift ILIKE $"+strconv.Itoa(paramCounter))
			args = append(args, "%"+filters.Shift.String+"%")
			paramCounter++
		}
		if filters.StartDate.Valid {
			whereConditions = append(whereConditions, "DATE(a.check_in_time) >= $"+strconv.Itoa(paramCounter))
			args = append(args, filters.StartDate.Time)
			paramCounter++
		}
		if filters.EndDate.Valid {
			whereConditions = append(whereConditions, "DATE(a.check_in_time) <= $"+strconv.Itoa(paramCounter))
			args = append(args, filters.EndDate.Time)
			paramCounter++
		}

		whereClause := ""
		if len(whereConditions) > 0 {
			whereClause = "WHERE " + strings.Join(whereConditions, " AND ")
		}

		query := `
		  SELECT a.id, a.assignment_id, a.check_in_time, a.check_out_time, a.lat, a.lng,
		         v.id AS volunteer_id, v.name AS volunteer_name, v.college_id AS volunteer_college_id, -- NEW
		         c.id AS committee_id, c.name AS committee_name,
		         e.id AS event_id, e.name AS event_name,
				 va.shift AS assignment_shift
		  FROM attendance a
		  JOIN volunteer_assignments va ON va.id = a.assignment_id
		  JOIN volunteers v ON v.id = va.volunteer_id
		  JOIN committees c ON c.id = va.committee_id
		  JOIN events e ON e.id = va.event_id
		  ` + whereClause + `
		  ORDER BY a.check_in_time DESC
		` // No LIMIT/OFFSET for CSV export

		rows, err := pool.Query(c.Context(), query, args...)
		if err != nil {
			log.Printf("Error querying attendance for CSV export: %v", err)
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to retrieve attendance data for export")
		}
		defer rows.Close()

		c.Set("Content-Type", "text/csv")
		c.Set("Content-Disposition", `attachment; filename="attendance_export.csv"`)

		writer := csv.NewWriter(c.Response().BodyWriter())
		defer writer.Flush()

		// Write CSV header
		header := []string{
			"Attendance ID", "Assignment ID", "Event ID", "Event Name", "Committee ID", "Committee Name",
			"Volunteer ID", "Volunteer Name", "Volunteer College ID", "Shift", "Check-in Time (ISO)", "Check-out Time (ISO)", "Latitude", "Longitude",
		} // NEW: Added Volunteer College ID
		if err := writer.Write(header); err != nil {
			log.Printf("Error writing CSV header: %v", err)
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to write CSV header")
		}

		// Write data rows
		for rows.Next() {
			var att models.Attendance
			var checkOutTime sql.NullTime
			var lat, lng sql.NullFloat64
			var volunteerName string
			var committeeName string
			var eventName string
			var assignmentShift sql.NullString
			var volunteerCollegeID sql.NullString // NEW

			err := rows.Scan(&att.ID, &att.AssignmentID, &att.CheckInTime, &checkOutTime, &lat, &lng,
				&att.VolunteerID, &volunteerName, &volunteerCollegeID, // NEW
				&att.CommitteeID, &committeeName,
				&att.EventID, &eventName,
				&assignmentShift)
			if err != nil {
				log.Printf("Error scanning attendance row for export: %v", err)
				continue // Skip this row, but continue with others
			}

			// Populate nullable fields from sql.NullXXX types
			if checkOutTime.Valid {
				att.CheckOutTime = &checkOutTime.Time
			}
			if lat.Valid {
				att.Lat = &lat.Float64
			}
			if lng.Valid {
				att.Lng = &lng.Float64
			}
			// The `Shift` field in `models.Attendance` is `*string`, so assign directly
			if assignmentShift.Valid {
				att.Shift = &assignmentShift.String
			}
			if volunteerCollegeID.Valid { // NEW
				att.VolunteerCollegeID = &volunteerCollegeID.String
			}

			checkOutTimeStr := ""
			if checkOutTime.Valid {
				checkOutTimeStr = checkOutTime.Time.Format(time.RFC3339)
			}

			record := []string{
				strconv.FormatInt(att.ID, 10),
				strconv.FormatInt(att.AssignmentID, 10),
				strconv.FormatInt(att.EventID, 10),
				eventName,
				strconv.FormatInt(att.CommitteeID, 10),
				committeeName,
				strconv.FormatInt(att.VolunteerID, 10),
				volunteerName,
				formatStringPtr(volunteerCollegeID), // NEW: The volunteer's college ID
				formatStringPtr(assignmentShift),    // The shift name
				att.CheckInTime.Format(time.RFC3339),
				checkOutTimeStr, // Use the properly formatted checkout time
				formatFloat64Ptr(lat),
				formatFloat64Ptr(lng),
			}
			if err := writer.Write(record); err != nil {
				log.Printf("Error writing CSV record for attendance ID %d: %v", att.ID, err)
			}
		}

		if err := rows.Err(); err != nil {
			log.Printf("Error iterating attendance rows for export: %v", err)
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to retrieve all attendance for export")
		}

		return nil
	}
}

// attendanceFilters struct for building dynamic queries
type attendanceFilters struct {
	EventID     sql.NullInt64
	CommitteeID sql.NullInt64
	VolunteerID sql.NullInt64
	Shift       sql.NullString
	StartDate   sql.NullTime
	EndDate     sql.NullTime
	Limit       int
	Offset      int
}

// buildAttendanceFilters parses query parameters into an attendanceFilters struct
func buildAttendanceFilters(c *fiber.Ctx) attendanceFilters {
	filters := attendanceFilters{}

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

// shiftCheckinFilters struct for building dynamic queries specific to shifts and dates
type shiftCheckinFilters struct {
	EventID     sql.NullInt64
	CommitteeID sql.NullInt64
	Shift       sql.NullString
	Date        sql.NullTime // Specific date for filtering
	Limit       int
	Offset      int
}

// buildShiftCheckinFilters parses query parameters for shift-based attendance endpoints
func buildShiftCheckinFilters(c *fiber.Ctx) shiftCheckinFilters {
	filters := shiftCheckinFilters{}

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

	shiftStr := c.Query("shift", "")
	if shiftStr != "" {
		filters.Shift = sql.NullString{String: shiftStr, Valid: true}
	}

	dateStr := c.Query("date", "")
	if dateStr != "" {
		if t, err := time.Parse("2006-01-02", dateStr); err == nil {
			filters.Date = sql.NullTime{Time: t, Valid: true}
		} else {
			// If date is invalid, set a default to prevent query errors, or return an error.
			// For simplicity, defaulting to today if provided but invalid.
			log.Printf("Warning: Could not parse date '%s': %v", dateStr, err) // Log the error
			filters.Date = sql.NullTime{Time: time.Now().Truncate(24 * time.Hour), Valid: true}
		}
	} else {
		// Default to today if no date is specified
		filters.Date = sql.NullTime{Time: time.Now().Truncate(24 * time.Hour), Valid: true}
	}

	filters.Limit = clampInt(c.QueryInt("limit", 100), 1, 500)
	filters.Offset = maxInt(c.QueryInt("offset", 0), 0)

	return filters
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

// Utility to safely format sql.NullTime to string
func formatTimePtr(nt sql.NullTime) string {
	if nt.Valid {
		return nt.Time.Format(time.RFC3339)
	}
	return ""
}

// Utility to safely format sql.NullInt64 to string
func formatInt64Ptr(ni sql.NullInt64) string {
	if ni.Valid {
		return strconv.FormatInt(ni.Int64, 10)
	}
	return ""
}

// Utility to safely format sql.NullString to string
func formatStringPtr(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

// Utility to safely format sql.NullFloat64 to string
func formatFloat64Ptr(nf sql.NullFloat64) string {
	if nf.Valid {
		return strconv.FormatFloat(nf.Float64, 'f', -1, 64)
	}
	return ""
}

// NEW: Filter struct for ListAssignmentsWithCheckinStatus
type assignmentStatusFilters struct {
	EventID     sql.NullInt64
	CommitteeID sql.NullInt64
	VolunteerID sql.NullInt64
	Shift       sql.NullString
	// Filters for the assignment's start/end times
	AssignmentStartDate sql.NullTime
	AssignmentEndDate   sql.NullTime
	// The specific date to check the attendance status against
	AttendanceCheckDate sql.NullTime
	Limit               int
	Offset              int
}

// NEW: Helper to parse query parameters for assignmentStatusFilters
func buildAssignmentStatusFilters(c *fiber.Ctx) assignmentStatusFilters {
	filters := assignmentStatusFilters{}

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

	assignmentStartDateStr := c.Query("assignment_start_date", "")
	if assignmentStartDateStr != "" {
		if t, err := time.Parse("2006-01-02", assignmentStartDateStr); err == nil {
			filters.AssignmentStartDate = sql.NullTime{Time: t, Valid: true}
		}
	}

	assignmentEndDateStr := c.Query("assignment_end_date", "")
	if assignmentEndDateStr != "" {
		if t, err := time.Parse("2006-01-02", assignmentEndDateStr); err == nil {
			filters.AssignmentEndDate = sql.NullTime{Time: t, Valid: true}
		}
	}

	attendanceCheckDateStr := c.Query("attendance_check_date", "")
	if attendanceCheckDateStr != "" {
		if t, err := time.Parse("2006-01-02", attendanceCheckDateStr); err == nil {
			filters.AttendanceCheckDate = sql.NullTime{Time: t, Valid: true}
		} else {
			// Log error but continue with default (today) if parse fails
			log.Printf("Warning: Could not parse attendance_check_date '%s': %v", attendanceCheckDateStr, err)
			filters.AttendanceCheckDate = sql.NullTime{Time: time.Now().Truncate(24 * time.Hour), Valid: true}
		}
	} else {
		// Default to today if no specific date is provided for attendance check
		filters.AttendanceCheckDate = sql.NullTime{Time: time.Now().Truncate(24 * time.Hour), Valid: true}
	}

	filters.Limit = clampInt(c.QueryInt("limit", 100), 1, 500)
	filters.Offset = maxInt(c.QueryInt("offset", 0), 0)

	return filters
}

// NEW: ListAssignmentsWithCheckinStatus - GET /attendance/assignments-status?event_id=&committee_id=&volunteer_id=&shift=&assignment_start_date=YYYY-MM-DD&assignment_end_date=YYYY-MM-DD&attendance_check_date=YYYY-MM-DD&limit=100&offset=0
// For Faculty/Admin to view all assignments with their check-in status for a specific day.
func ListAssignmentsWithCheckinStatus(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		filters := buildAssignmentStatusFilters(c)

		args := []any{}
		whereConditions := []string{}
		paramCounter := 1

		if filters.EventID.Valid {
			whereConditions = append(whereConditions, "va.event_id=$"+strconv.Itoa(paramCounter))
			args = append(args, filters.EventID.Int64)
			paramCounter++
		}
		if filters.CommitteeID.Valid {
			whereConditions = append(whereConditions, "va.committee_id=$"+strconv.Itoa(paramCounter))
			args = append(args, filters.CommitteeID.Int64)
			paramCounter++
		}
		if filters.VolunteerID.Valid {
			whereConditions = append(whereConditions, "va.volunteer_id=$"+strconv.Itoa(paramCounter))
			args = append(args, filters.VolunteerID.Int64)
			paramCounter++
		}
		if filters.Shift.Valid {
			whereConditions = append(whereConditions, "va.shift ILIKE $"+strconv.Itoa(paramCounter))
			args = append(args, "%"+filters.Shift.String+"%")
			paramCounter++
		}
		if filters.AssignmentStartDate.Valid {
			whereConditions = append(whereConditions, "DATE(va.start_time) >= $"+strconv.Itoa(paramCounter))
			args = append(args, filters.AssignmentStartDate.Time)
			paramCounter++
		}
		if filters.AssignmentEndDate.Valid {
			whereConditions = append(whereConditions, "DATE(va.start_time) <= $"+strconv.Itoa(paramCounter))
			args = append(args, filters.AssignmentEndDate.Time)
			paramCounter++
		}

		whereClause := ""
		if len(whereConditions) > 0 {
			whereClause = "WHERE " + strings.Join(whereConditions, " AND ")
		}

		// Add parameter for the attendance check date (or CURRENT_DATE if not provided)
		attendanceCheckDateParam := sql.NullTime{}
		if filters.AttendanceCheckDate.Valid {
			attendanceCheckDateParam = filters.AttendanceCheckDate
		} else {
			// This branch should ideally not be hit if buildAssignmentStatusFilters defaults to today
			attendanceCheckDateParam = sql.NullTime{Time: time.Now().Truncate(24 * time.Hour), Valid: true}
		}
		args = append(args, attendanceCheckDateParam.Time) // Add attendance check date to args
		attendanceCheckDatePlaceholder := "$" + strconv.Itoa(paramCounter)
		paramCounter++

		// Add limit/offset
		args = append(args, filters.Limit, filters.Offset)
		query := `
		  SELECT
		    va.id, va.event_id, va.committee_id, va.volunteer_id,
		    va.role::text, va.status::text, va.reporting_time, va.shift, va.start_time, va.end_time, va.notes, va.created_at,
		    v.name AS volunteer_name, v.email AS volunteer_email, v.college_id AS volunteer_college_id, -- NEW
		    c.name AS committee_name,
		    e.name AS event_name,
		    (
		        SELECT att.id
		        FROM attendance att
		        WHERE att.assignment_id = va.id
		          AND DATE(att.check_in_time) = ` + attendanceCheckDatePlaceholder + `
		          AND att.check_out_time IS NULL
		        LIMIT 1
		    ) AS active_attendance_id
		  FROM volunteer_assignments va
		  JOIN volunteers v ON v.id = va.volunteer_id
		  JOIN committees c ON c.id = va.committee_id
		  JOIN events e ON e.id = va.event_id
		  ` + whereClause + `
		  ORDER BY va.event_id, va.committee_id, va.start_time, v.name ASC
		  LIMIT $` + strconv.Itoa(paramCounter) + ` OFFSET $` + strconv.Itoa(paramCounter+1)

		rows, err := pool.Query(c.Context(), query, args...)
		if err != nil {
			log.Printf("Error querying assignments with check-in status: %v", err)
			return err
		}
		defer rows.Close()

		out := make([]models.AssignmentWithCheckinStatus, 0, filters.Limit)
		for rows.Next() {
			var assignment models.AssignmentWithCheckinStatus
			var roleStr, statusStr string
			var activeAttendanceID sql.NullInt64
			var volunteerEmail, volunteerCollegeID sql.NullString // NEW

			err := rows.Scan(
				&assignment.ID, &assignment.EventID, &assignment.CommitteeID, &assignment.VolunteerID,
				&roleStr, &statusStr, &assignment.ReportingTime, &assignment.Shift, &assignment.StartTime, &assignment.EndTime, &assignment.Notes, &assignment.CreatedAt,
				&assignment.VolunteerName, &volunteerEmail, &volunteerCollegeID, &assignment.CommitteeName, &assignment.EventName, // NEW
				&activeAttendanceID, // Scan the result of the subquery
			)
			if err != nil {
				log.Printf("Error scanning assignment with check-in status row: %v", err)
				return err
			}

			assignment.Role = models.AssignmentRole(roleStr)
			assignment.Status = models.AssignmentStatus(statusStr)
			assignment.VolunteerEmail = derefNullString(volunteerEmail)         // NEW
			assignment.VolunteerCollegeID = derefNullString(volunteerCollegeID) // NEW
			assignment.ActiveAttendanceID = activeAttendanceID
			assignment.IsCheckedIn = activeAttendanceID.Valid // If ActiveAttendanceID is valid, they are checked in

			out = append(out, assignment)
		}
		if err := rows.Err(); err != nil {
			log.Printf("Error iterating assignments with check-in status rows: %v", err)
			return err
		}
		return c.JSON(out)
	}
}
func derefNullString(s sql.NullString) *string {
	if s.Valid {
		return &s.String
	}
	return nil
}
