package main

import (
	"log"
	"os"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/joho/godotenv"

	"Seva-app-backend/db"
	hAnnounce "Seva-app-backend/handlers/announcements"
	hAttendance "Seva-app-backend/handlers/attendance"
	hauth "Seva-app-backend/handlers/auth"
	hCommittees "Seva-app-backend/handlers/committees"
	"Seva-app-backend/handlers/health"
	hlocations "Seva-app-backend/handlers/locations"
	hQuestions "Seva-app-backend/handlers/questions"
	hVolunteers "Seva-app-backend/handlers/volunteers"
	mw "Seva-app-backend/middleware"
	"Seva-app-backend/models"
)

func main() {
	_ = godotenv.Load()

	addr := os.Getenv("API_ADDR")
	if addr == "" {
		addr = ":8000"
	}

	pool := db.MustPool()
	defer pool.Close()

	app := fiber.New()
	app.Use(recover.New())
	app.Use(logger.New())
	// Optional: Add the custom routing debug middleware again to confirm the fix
	app.Use(func(c *fiber.Ctx) error {
		log.Printf("ROUTING DEBUG: Method: %s, Path: %s, OriginalURL: %s", c.Method(), c.Path(), c.OriginalURL())
		return c.Next()
	})
	app.Use(cors.New(cors.Config{
		AllowOrigins: "*",
		AllowHeaders: "Origin, Content-Type, Accept, Authorization",
		AllowMethods: "GET,POST,HEAD,PUT,DELETE,PATCH",
	}))

	app.Get("/healthz", health.Health())

	// JWT Guards and Role Requirements
	jwtGuard := mw.JwtGuard()
	requireAdmin := mw.RequireRole(string(models.UserRoleAdmin))
	requireFaculty := mw.RequireRole(string(models.UserRoleFaculty), string(models.UserRoleAdmin))
	requireVolunteer := mw.RequireRole(string(models.UserRoleVolunteer), string(models.UserRoleAdmin))

	// --- Auth routes ---
	authGroup := app.Group("/auth")
	hauth.Register(authGroup, pool, jwtGuard, requireAdmin)

	// --- Committees ---
	comm := app.Group("/committees")
	comm.Get("/", hCommittees.List(pool))
	comm.Get("/:id", hCommittees.Get(pool))
	comm.Post("/", jwtGuard, requireAdmin, hCommittees.Create(pool))
	comm.Put("/:id", jwtGuard, requireAdmin, hCommittees.Update(pool))
	comm.Delete("/:id", jwtGuard, requireAdmin, hCommittees.Del(pool))

	// --- Volunteers ---
	vol := app.Group("/volunteers")
	// IMPORTANT: Define more specific static routes BEFORE general parameter routes
	// Admin-only Bulk Operations (static paths)
	vol.Post("/bulk", jwtGuard, requireAdmin, hVolunteers.BulkUpload(pool))
	vol.Get("/export_csv", jwtGuard, requireAdmin, hVolunteers.ExportVolunteersCSV(pool))
	vol.Get("/assignments/export_csv", jwtGuard, requireAdmin, hVolunteers.ExportAssignmentsCSV(pool))

	// Admin-only Assignment Management (static paths, then parameter paths)
	vol.Post("/assignments", jwtGuard, requireAdmin, hVolunteers.CreateAssignment(pool))
	vol.Get("/assignments", jwtGuard, requireAdmin, hVolunteers.ListAssignments(pool))       // This must be BEFORE /:id
	vol.Get("/assignments/:id", jwtGuard, requireAdmin, hVolunteers.GetAssignmentByID(pool)) // This is specific for /assignments/N
	vol.Put("/assignments/:id", jwtGuard, requireAdmin, hVolunteers.UpdateAssignment(pool))
	vol.Delete("/assignments/:id", jwtGuard, requireAdmin, hVolunteers.DeleteAssignment(pool))

	// General volunteer management (static path for list, then parameter for ID)
	vol.Post("/", jwtGuard, requireAdmin, hVolunteers.CreateSingle(pool))
	vol.Get("/", jwtGuard, requireAdmin, hVolunteers.ListVolunteers(pool)) // This is for /volunteers

	// Volunteer specific "me" routes (static paths)
	vol.Get("/me", jwtGuard, requireVolunteer, hVolunteers.GetMyProfile(pool))
	vol.Post("/me/set-password", jwtGuard, requireVolunteer, hVolunteers.SetMyPassword(pool))
	vol.Get("/me/assignments", jwtGuard, requireVolunteer, hVolunteers.GetMyAssignments(pool))
	vol.Get("/me/committees", jwtGuard, requireVolunteer, hVolunteers.GetMyCommittees(pool))

	// FINALLY, the general /:id route for volunteers
	// This must come AFTER all other static paths like /assignments, /me, /bulk etc.
	vol.Get("/:id", jwtGuard, requireAdmin, hVolunteers.GetVolunteerByID(pool))
	vol.Put("/:id", jwtGuard, requireAdmin, hVolunteers.UpdateVolunteer(pool))
	vol.Delete("/:id", jwtGuard, requireAdmin, hVolunteers.DeleteVolunteer(pool))

	// --- Attendance ---
	att := app.Group("/attendance")
	hAttendance.Register(att, pool, jwtGuard, requireFaculty, requireVolunteer)

	// --- Announcements ---
	ann := app.Group("/announcements")
	ann.Post("/", jwtGuard, requireAdmin, hAnnounce.Create(pool))
	ann.Put("/:id", jwtGuard, requireAdmin, hAnnounce.Update(pool))
	ann.Delete("/:id", jwtGuard, requireAdmin, hAnnounce.Del(pool))
	ann.Get("/", jwtGuard, requireFaculty, hAnnounce.ListAll(pool))
	ann.Get("/:id", jwtGuard, requireFaculty, hAnnounce.Get(pool))
	ann.Get("/me", jwtGuard, requireVolunteer, hAnnounce.ListForVolunteer(pool))

	// --- Locations ---
	loc := app.Group("/locations")
	loc.Post("/", jwtGuard, requireAdmin, hlocations.CreateLocation(pool))
	loc.Put("/:id", jwtGuard, requireAdmin, hlocations.UpdateLocation(pool))
	loc.Delete("/:id", jwtGuard, requireAdmin, hlocations.DeleteLocation(pool))
	loc.Get("/", hlocations.ListLocations(pool))
	loc.Get("/:id", hlocations.GetLocationByID(pool))

	// --- Questions (May I Help You) ---
	qa := app.Group("/questions")
	hQuestions.Register(qa, pool, jwtGuard, requireAdmin, requireVolunteer)

	log.Printf("listening on %s", addr)
	log.Fatal(app.Listen(addr))
}
