package questions

import (
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	mw "Seva-app-backend/middleware"
	"Seva-app-backend/models"
)

// Register mounts question routes under /questions
func Register(g fiber.Router, pool *pgxpool.Pool, jwtGuard fiber.Handler, requireAdmin fiber.Handler, requireVolunteer fiber.Handler) {
	// Volunteer Endpoints
	g.Post("/", jwtGuard, requireVolunteer, AskQuestion(pool))
	g.Get("/me", jwtGuard, requireVolunteer, ListMyQuestions(pool))
	g.Get("/answered", ListAnsweredQuestions(pool)) // Public/Logged-in can see general FAQ

	// Admin Endpoints
	g.Get("/all", jwtGuard, requireAdmin, ListAllQuestions(pool))
	g.Get("/pending", jwtGuard, requireAdmin, ListPendingQuestions(pool))
	g.Put("/:id/answer", jwtGuard, requireAdmin, AnswerQuestion(pool))
	g.Delete("/:id", jwtGuard, requireAdmin, DeleteQuestion(pool))
}

// AskQuestion - POST /questions (Volunteer)
func AskQuestion(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		volunteerID, err := mw.GetUserIDFromClaims(c)
		if err != nil {
			return fiber.NewError(fiber.StatusUnauthorized, "Volunteer ID not found in token")
		}

		var req models.CreateQuestionRequest
		if err := c.BodyParser(&req); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Bad JSON")
		}
		if strings.TrimSpace(req.QuestionText) == "" {
			return fiber.NewError(fiber.StatusBadRequest, "Question text is required")
		}

		var newQuestion models.Question
		err = pool.QueryRow(c.Context(), `
			INSERT INTO questions(volunteer_id, question_text, event_id, committee_id)
			VALUES ($1, $2, $3, $4)
			RETURNING id, volunteer_id, question_text, asked_at, event_id, committee_id
		`, volunteerID, req.QuestionText, req.EventID, req.CommitteeID).Scan(
			&newQuestion.ID, &newQuestion.VolunteerID, &newQuestion.QuestionText, &newQuestion.AskedAt,
			&newQuestion.EventID, &newQuestion.CommitteeID,
		)
		if err != nil {
			return err
		}
		return c.Status(fiber.StatusCreated).JSON(newQuestion)
	}
}

// ListMyQuestions - GET /questions/me (Volunteer)
func ListMyQuestions(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		volunteerID, err := mw.GetUserIDFromClaims(c)
		if err != nil {
			return fiber.NewError(fiber.StatusUnauthorized, "Volunteer ID not found in token")
		}

		limit := clampInt(c.QueryInt("limit", 100), 1, 500)
		offset := maxInt(c.QueryInt("offset", 0), 0)

		rows, err := pool.Query(c.Context(), `
			SELECT q.id, q.volunteer_id, v.name, q.question_text, q.asked_at,
				   q.event_id, q.committee_id, q.answered_by, f.name, q.answer_text, q.answered_at
			FROM questions q
			JOIN volunteers v ON v.id = q.volunteer_id
			LEFT JOIN faculty f ON f.id = q.answered_by
			WHERE q.volunteer_id = $1
			ORDER BY q.asked_at DESC
			LIMIT $2 OFFSET $3
		`, volunteerID, limit, offset)
		if err != nil {
			return err
		}
		defer rows.Close()

		questions := []models.Question{}
		for rows.Next() {
			var q models.Question
			if err := rows.Scan(
				&q.ID, &q.VolunteerID, &q.VolunteerName, &q.QuestionText, &q.AskedAt,
				&q.EventID, &q.CommitteeID, &q.AnsweredBy, &q.AnsweredByName, &q.AnswerText, &q.AnsweredAt,
			); err != nil {
				return err
			}
			questions = append(questions, q)
		}
		return c.JSON(questions)
	}
}

// ListAnsweredQuestions - GET /questions/answered (Public/Volunteer)
// Shows all questions that have been answered. Can be used as a public FAQ.
func ListAnsweredQuestions(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		limit := clampInt(c.QueryInt("limit", 100), 1, 500)
		offset := maxInt(c.QueryInt("offset", 0), 0)

		rows, err := pool.Query(c.Context(), `
			SELECT q.id, q.volunteer_id, v.name, q.question_text, q.asked_at,
				   q.event_id, q.committee_id, q.answered_by, f.name, q.answer_text, q.answered_at
			FROM questions q
			LEFT JOIN volunteers v ON v.id = q.volunteer_id
			LEFT JOIN faculty f ON f.id = q.answered_by
			WHERE q.answer_text IS NOT NULL
			ORDER BY q.answered_at DESC
			LIMIT $1 OFFSET $2
		`, limit, offset)
		if err != nil {
			return err
		}
		defer rows.Close()

		questions := []models.Question{}
		for rows.Next() {
			var q models.Question
			if err := rows.Scan(
				&q.ID, &q.VolunteerID, &q.VolunteerName, &q.QuestionText, &q.AskedAt,
				&q.EventID, &q.CommitteeID, &q.AnsweredBy, &q.AnsweredByName, &q.AnswerText, &q.AnsweredAt,
			); err != nil {
				return err
			}
			questions = append(questions, q)
		}
		return c.JSON(questions)
	}
}

// ListAllQuestions - GET /questions/all (Admin)
func ListAllQuestions(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		limit := clampInt(c.QueryInt("limit", 100), 1, 500)
		offset := maxInt(c.QueryInt("offset", 0), 0)

		rows, err := pool.Query(c.Context(), `
			SELECT q.id, q.volunteer_id, v.name, q.question_text, q.asked_at,
				   q.event_id, q.committee_id, q.answered_by, f.name, q.answer_text, q.answered_at
			FROM questions q
			LEFT JOIN volunteers v ON v.id = q.volunteer_id
			LEFT JOIN faculty f ON f.id = q.answered_by
			ORDER BY q.asked_at DESC
			LIMIT $1 OFFSET $2
		`, limit, offset)
		if err != nil {
			return err
		}
		defer rows.Close()

		questions := []models.Question{}
		for rows.Next() {
			var q models.Question
			if err := rows.Scan(
				&q.ID, &q.VolunteerID, &q.VolunteerName, &q.QuestionText, &q.AskedAt,
				&q.EventID, &q.CommitteeID, &q.AnsweredBy, &q.AnsweredByName, &q.AnswerText, &q.AnsweredAt,
			); err != nil {
				return err
			}
			questions = append(questions, q)
		}
		return c.JSON(questions)
	}
}

// ListPendingQuestions - GET /questions/pending (Admin)
func ListPendingQuestions(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		limit := clampInt(c.QueryInt("limit", 100), 1, 500)
		offset := maxInt(c.QueryInt("offset", 0), 0)

		rows, err := pool.Query(c.Context(), `
			SELECT q.id, q.volunteer_id, v.name, q.question_text, q.asked_at,
				   q.event_id, q.committee_id, q.answered_by, f.name, q.answer_text, q.answered_at
			FROM questions q
			LEFT JOIN volunteers v ON v.id = q.volunteer_id
			LEFT JOIN faculty f ON f.id = q.answered_by
			WHERE q.answer_text IS NULL
			ORDER BY q.asked_at DESC
			LIMIT $1 OFFSET $2
		`, limit, offset)
		if err != nil {
			return err
		}
		defer rows.Close()

		questions := []models.Question{}
		for rows.Next() {
			var q models.Question
			if err := rows.Scan(
				&q.ID, &q.VolunteerID, &q.VolunteerName, &q.QuestionText, &q.AskedAt,
				&q.EventID, &q.CommitteeID, &q.AnsweredBy, &q.AnsweredByName, &q.AnswerText, &q.AnsweredAt,
			); err != nil {
				return err
			}
			questions = append(questions, q)
		}
		return c.JSON(questions)
	}
}

// AnswerQuestion - PUT /questions/:id/answer (Admin)
func AnswerQuestion(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		questionID, err := strconv.ParseInt(c.Params("id"), 10, 64)
		if err != nil || questionID <= 0 {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid question ID")
		}

		adminID, err := mw.GetUserIDFromClaims(c)
		if err != nil {
			return fiber.NewError(fiber.StatusUnauthorized, "Admin ID not found in token")
		}

		var req models.AnswerQuestionRequest
		if err := c.BodyParser(&req); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Bad JSON")
		}
		if strings.TrimSpace(req.AnswerText) == "" {
			return fiber.NewError(fiber.StatusBadRequest, "Answer text is required")
		}

		now := time.Now()
		cmd, err := pool.Exec(c.Context(), `
			UPDATE questions
			SET answer_text = $1, answered_by = $2, answered_at = $3
			WHERE id = $4 AND answer_text IS NULL
		`, req.AnswerText, adminID, now, questionID)
		if err != nil {
			return err
		}
		if cmd.RowsAffected() == 0 {
			var exists bool
			_ = pool.QueryRow(c.Context(), `SELECT EXISTS(SELECT 1 FROM questions WHERE id = $1)`, questionID).Scan(&exists)
			if !exists {
				return fiber.NewError(fiber.StatusNotFound, "Question not found")
			}
			return fiber.NewError(fiber.StatusConflict, "Question already answered")
		}
		return c.Status(fiber.StatusNoContent).JSON(fiber.Map{"message": "Question answered successfully", "answered_at": now})
	}
}

// DeleteQuestion - DELETE /questions/:id (Admin)
func DeleteQuestion(pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		questionID, err := strconv.ParseInt(c.Params("id"), 10, 64)
		if err != nil || questionID <= 0 {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid question ID")
		}

		cmd, err := pool.Exec(c.Context(), `DELETE FROM questions WHERE id = $1`, questionID)
		if err != nil {
			return err
		}
		if cmd.RowsAffected() == 0 {
			return fiber.NewError(fiber.StatusNotFound, "Question not found")
		}
		return c.SendStatus(fiber.StatusNoContent)
	}
}

// Helpers
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
