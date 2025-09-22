const API_BASE_URL = "http://localhost:8000"; // IMPORTANT: Update if your backend runs on a different port/host

// --- Global State ---
let currentUser = null;
let currentRole = null;
let accessToken = null;

// --- DOM Elements ---
const messageContainer = document.getElementById('message-container');
const sections = document.querySelectorAll('main section');
const navButtons = {
    home: document.getElementById('nav-home'),
    myAssignments: document.getElementById('nav-my-assignments'),
    myCommittees: document.getElementById('nav-my-committees'),
    myAnnouncements: document.getElementById('nav-my-announcements'),
    myQuestions: document.getElementById('nav-my-questions'),
    askQuestion: document.getElementById('nav-ask-question'),
    adminDashboard: document.getElementById('nav-admin-dashboard'),
    logout: document.getElementById('nav-logout'),
    login: document.getElementById('nav-login')
};

// --- API Client ---
const apiClient = {
    request: async (method, path, data = null, isFormData = false) => {
        const headers = {};
        if (accessToken) {
            headers['Authorization'] = `Bearer ${accessToken}`;
        }

        const config = { method };

        if (isFormData) {
            config.body = data; // FormData handles its own Content-Type
        } else if (data) {
            headers['Content-Type'] = 'application/json';
            config.body = JSON.stringify(data);
        }
        config.headers = headers;

        try {
            const response = await fetch(`${API_BASE_URL}${path}`, config);
            if (!response.ok) {
                const errorData = await response.json();
                throw new Error(errorData.error || `API Error: ${response.statusText}`);
            }
            if (response.status === 204) return null; // No Content
            return await response.json();
        } catch (error) {
            console.error("API request failed:", error);
            throw error;
        }
    },
    auth: {
        login: (email, password) => apiClient.request('POST', '/auth/login', { email, password }),
        registerVolunteer: (data) => apiClient.request('POST', '/auth/register/volunteer', data),
        logout: () => apiClient.request('POST', '/auth/logout'),
        me: () => apiClient.request('GET', '/auth/me'),
        setPassword: (data) => apiClient.request('POST', '/volunteers/me/set-password', data),
    },
    volunteers: {
        // Volunteer-specific
        myProfile: () => apiClient.request('GET', '/volunteers/me'),
        myAssignments: () => apiClient.request('GET', '/volunteers/me/assignments'),
        myCommittees: () => apiClient.request('GET', '/volunteers/me/committees'),
        myAnnouncements: () => apiClient.request('GET', '/announcements/me'),

        // Admin-specific
        create: (data) => apiClient.request('POST', '/volunteers', data),
        listAll: () => apiClient.request('GET', '/volunteers'),
        bulkUpload: (eventId, committeeId, formData) => apiClient.request('POST', `/volunteers/bulk?event_id=${eventId}&committee_id=${committeeId}`, formData, true),
        exportVolunteersCSV: () => apiClient.request('GET', '/volunteers/export_csv'),
        exportAssignmentsCSV: () => apiClient.request('GET', '/volunteers/assignments/export_csv'),
        listAllAssignments: () => apiClient.request('GET', '/volunteers/assignments'),
    },
    attendance: {
        checkIn: (assignmentId, lat, lng) => apiClient.request('POST', '/attendance/checkin', { assignment_id: assignmentId, lat, lng }),
        checkOut: (attendanceId) => apiClient.request('POST', '/attendance/checkout', { attendance_id: attendanceId }),
        pending: (eventId, committeeId) => apiClient.request('GET', `/attendance/pending?event_id=${eventId}&committee_id=${committeeId}`),
        approve: (attendanceId, approvedBy) => apiClient.request('POST', '/attendance/approve', { attendance_id: attendanceId, approved_by: approvedBy }),
    },
    questions: {
        // Volunteer-specific
        ask: (questionText, eventId, committeeId) => apiClient.request('POST', '/questions', { question_text: questionText, event_id: eventId, committee_id: committeeId }),
        myQuestions: () => apiClient.request('GET', '/questions/me'),
        // Public / general
        listAnswered: () => apiClient.request('GET', '/questions/answered'),
        // Admin-specific
        listAll: () => apiClient.request('GET', '/questions/all'),
        listPending: () => apiClient.request('GET', '/questions/pending'),
        answer: (questionId, answerText) => apiClient.request('PUT', `/questions/${questionId}/answer`, { answer_text: answerText }),
    }
};

// --- Utility Functions ---
function displayMessage(type, text) {
    messageContainer.className = `message-container ${type}`;
    messageContainer.textContent = text;
    messageContainer.style.display = 'block';
    setTimeout(() => {
        messageContainer.style.display = 'none';
    }, 5000); // Hide after 5 seconds
}

function hideAllSections() {
    sections.forEach(section => section.classList.add('hidden'));
}

function showSection(id) {
    hideAllSections();
    document.getElementById(id).classList.remove('hidden');
}

function updateNavVisibility() {
    const isAdmin = currentRole === 'admin';
    const isFaculty = currentRole === 'faculty';
    const isVolunteer = currentRole === 'volunteer';
    const isLoggedIn = !!accessToken;

    Object.values(navButtons).forEach(btn => btn.classList.add('hidden')); // Hide all first

    if (isLoggedIn) {
        navButtons.home.classList.remove('hidden');
        navButtons.logout.classList.remove('hidden');

        if (isAdmin) {
            navButtons.adminDashboard.classList.remove('hidden');
        }
        if (isVolunteer) { // Volunteer-specific buttons
            navButtons.myAssignments.classList.remove('hidden');
            navButtons.myCommittees.classList.remove('hidden');
            navButtons.myAnnouncements.classList.remove('hidden');
            navButtons.myQuestions.classList.remove('hidden');
            navButtons.askQuestion.classList.remove('hidden');
        }
        // Faculty-specific nav items would go here if different from AdminDashboard
    } else {
        navButtons.login.classList.remove('hidden');
    }
}

function parseJwt(token) {
    try {
        const base64Url = token.split('.')[1];
        const base64 = base64Url.replace(/-/g, '+').replace(/_/g, '/');
        const jsonPayload = decodeURIComponent(atob(base64).split('').map(function(c) {
            return '%' + ('00' + c.charCodeAt(0).toString(16)).slice(-2);
        }).join(''));
        return JSON.parse(jsonPayload);
    } catch (e) {
        console.error("Error parsing JWT:", e);
        return null;
    }
}

function setAuthData(token) {
    accessToken = token;
    localStorage.setItem('accessToken', token);
    const claims = parseJwt(token);
    if (claims) {
        currentUser = claims.sub;
        currentRole = claims.role;
        localStorage.setItem('currentUser', currentUser);
        localStorage.setItem('currentRole', currentRole);
        console.log("Auth data set:", { currentUser, currentRole }); // Debugging log
    } else {
        console.warn("Could not parse JWT claims from token:", token); // Debugging log
    }
    renderApp();
}

function clearAuthData() {
    accessToken = null;
    currentUser = null;
    currentRole = null;
    localStorage.removeItem('accessToken');
    localStorage.removeItem('currentUser');
    localStorage.removeItem('currentRole');
    console.log("Auth data cleared."); // Debugging log
    renderApp();
}

// --- Render Functions ---

// --- Admin Dashboard Logic ---
async function showAdminDashboard() {
    console.log("Attempting to show Admin Dashboard..."); // Debugging log
    showSection('admin-dashboard-section');
    await loadAdminDashboardData();
}

// --- Volunteer Dashboard Logic ---
async function showVolunteerDashboard() {
    console.log("Attempting to show Volunteer Dashboard..."); // Debugging log
    showSection('volunteer-dashboard-section');
    document.getElementById('my-profile-role').textContent = currentRole;

    try {
        const profile = await apiClient.volunteers.myProfile();
        document.getElementById('my-profile-id').textContent = profile.id;
        document.getElementById('my-profile-name').textContent = profile.name;
        document.getElementById('my-profile-email').textContent = profile.email || 'N/A';
        document.getElementById('my-profile-phone').textContent = profile.phone || 'N/A';
        document.getElementById('my-profile-dept').textContent = profile.dept || 'N/A';
        document.getElementById('my-profile-college-id').textContent = profile.college_id || 'N/A';
        console.log("Volunteer profile loaded."); // Debugging log
    } catch (error) {
        displayMessage('error', 'Failed to load profile: ' + error.message);
        console.error("Error loading volunteer profile:", error); // Debugging log
    }
}


function renderApp() {
    console.log("renderApp called. Current Role:", currentRole, "Is Logged In:", !!accessToken); // Debugging log
    updateNavVisibility();

    if (!accessToken) {
        showSection('auth-section');
        console.log("Not logged in, showing auth section."); // Debugging log
        return;
    }

    // Default view based on role
    if (currentRole === 'admin') {
        showAdminDashboard();
    } else if (currentRole === 'volunteer') {
        showVolunteerDashboard();
    } else if (currentRole === 'faculty') {
        // Faculty dashboard can be implemented here, for now, default to admin-like view if permitted
        showAdminDashboard(); // Faculty often have admin-like views for their domain
    } else {
        // Fallback in case of an unexpected role or parse error
        showSection('auth-section');
        displayMessage('error', 'Unknown user role. Please log in again.');
        console.warn("Unknown user role:", currentRole); // Debugging log
        clearAuthData(); // Clear potentially corrupted session
    }
}

// --- Auth Handlers ---
document.getElementById('login-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const email = document.getElementById('login-email').value;
    const password = document.getElementById('login-password').value;
    console.log("Attempting login for:", email); // Debugging log

    try {
        const data = await apiClient.auth.login(email, password);
        setAuthData(data.access_token);
        displayMessage('success', `Logged in as ${data.role}!`);
    } catch (error) {
        displayMessage('error', error.message);
        console.error("Login failed:", error); // Debugging log
    }
});

document.getElementById('register-volunteer-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const data = {
        name: document.getElementById('register-name').value,
        email: document.getElementById('register-email').value,
        password: document.getElementById('register-password').value,
        phone: document.getElementById('register-phone').value || undefined,
        dept: document.getElementById('register-dept').value || undefined,
        college_id: document.getElementById('register-college-id').value || undefined,
    };
    console.log("Attempting volunteer registration for:", data.email); // Debugging log

    try {
        await apiClient.auth.registerVolunteer(data);
        displayMessage('success', 'Volunteer registered successfully! Please log in.');
        document.getElementById('register-volunteer-form').reset();
    } catch (error) {
        displayMessage('error', error.message);
        console.error("Registration failed:", error); // Debugging log
    }
});

navButtons.logout.addEventListener('click', async () => {
    console.log("Logout initiated."); // Debugging log
    try {
        await apiClient.auth.logout();
        displayMessage('success', 'Logged out.');
    } catch (error) {
        console.error("Logout API failed, but clearing local session.", error);
        displayMessage('error', 'Logout failed, but session cleared locally.');
    } finally {
        clearAuthData();
    }
});

navButtons.login.addEventListener('click', () => {
    console.log("Navigating to login/register section."); // Debugging log
    showSection('auth-section');
});

// --- Volunteer Dashboard specific event listeners ---
document.getElementById('show-set-password-form').addEventListener('click', () => {
    document.getElementById('set-password-form-container').classList.toggle('hidden');
});

document.getElementById('set-password-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const oldPassword = document.getElementById('old-password').value;
    const newPassword = document.getElementById('new-password').value;

    try {
        const payload = { new_password: newPassword };
        if (oldPassword) payload.old_password = oldPassword;

        await apiClient.auth.setPassword(payload);
        displayMessage('success', 'Password updated successfully!');
        document.getElementById('set-password-form').reset();
        document.getElementById('set-password-form-container').classList.add('hidden');
    } catch (error) {
        displayMessage('error', 'Failed to update password: ' + error.message);
        console.error("Set password failed:", error); // Debugging log
    }
});

// --- My Assignments ---
navButtons.myAssignments.addEventListener('click', async () => {
    console.log("Navigating to My Assignments."); // Debugging log
    showSection('my-assignments-section');
    const container = document.getElementById('my-assignments-list');
    container.innerHTML = 'Loading assignments...';

    try {
        const assignments = await apiClient.volunteers.myAssignments();
        if (assignments.length === 0) {
            container.innerHTML = '<p>You have no assignments.</p>';
            return;
        }
        renderAssignmentsTable(container, assignments, true); // true for volunteer view
    } catch (error) {
        displayMessage('error', 'Failed to load assignments: ' + error.message);
        container.innerHTML = '<p>Error loading assignments.</p>';
        console.error("Error loading my assignments:", error); // Debugging log
    }
});

function renderAssignmentsTable(container, assignments, isVolunteerView = false) {
    let html = `
        <table>
            <thead>
                <tr>
                    <th>Event</th>
                    <th>Committee</th>
                    <th>Role</th>
                    <th>Status</th>
                    <th>Shift</th>
                    <th>Start Time</th>
                    <th>End Time</th>
                    <th>Reporting Time</th>
                    <th>Notes</th>
                    ${isVolunteerView ? '<th>Actions</th>' : ''}
                </tr>
            </thead>
            <tbody>
    `;

    for (const assignment of assignments) {
        html += `
            <tr>
                <td>${assignment.event_name || 'N/A'}</td>
                <td>${assignment.committee_name || 'N/A'}</td>
                <td>${assignment.role}</td>
                <td>${assignment.status}</td>
                <td>${assignment.shift || 'N/A'}</td>
                <td>${assignment.start_time ? new Date(assignment.start_time).toLocaleString() : 'N/A'}</td>
                <td>${assignment.end_time ? new Date(assignment.end_time).toLocaleString() : 'N/A'}</td>
                <td>${assignment.reporting_time ? new Date(assignment.reporting_time).toLocaleString() : 'N/A'}</td>
                <td>${assignment.notes || 'N/A'}</td>
                ${isVolunteerView ? `
                    <td class="attendance-buttons">
                        <button class="checkin-button" data-assignment-id="${assignment.id}">Check In</button>
                        <button class="checkout-button" data-assignment-id="${assignment.id}" disabled>Check Out (not implemented yet)</button>
                    </td>
                ` : ''}
            </tr>
        `;
    }
    html += `</tbody></table>`;
    container.innerHTML = html;

    if (isVolunteerView) {
        container.querySelectorAll('.checkin-button').forEach(button => {
            button.addEventListener('click', async () => {
                const assignmentId = parseInt(button.dataset.assignmentId);
                // For simplicity, we'll use dummy lat/lng, in real app, use geolocation API
                const lat = 0.0;
                const lng = 0.0;
                try {
                    await apiClient.attendance.checkIn(assignmentId, lat, lng);
                    displayMessage('success', `Checked in for assignment ${assignmentId}!`);
                    console.log(`Checked in for assignment ${assignmentId}.`); // Debugging log
                    // Re-render assignments after check-in to reflect status/buttons, or fetch current attendance ID for checkout
                    setTimeout(() => navButtons.myAssignments.click(), 1000);
                } catch (error) {
                    displayMessage('error', `Check-in failed: ${error.message}`);
                    console.error(`Check-in failed for assignment ${assignmentId}:`, error); // Debugging log
                }
            });
        });
    }
}

// --- My Committees ---
navButtons.myCommittees.addEventListener('click', async () => {
    console.log("Navigating to My Committees."); // Debugging log
    showSection('my-committees-section');
    const container = document.getElementById('my-committees-list');
    container.innerHTML = 'Loading committees...';

    try {
        const committees = await apiClient.volunteers.myCommittees();
        if (committees.length === 0) {
            container.innerHTML = '<p>You are not assigned to any committees.</p>';
            return;
        }
        let html = '<table><thead><tr><th>ID</th><th>Event Name</th><th>Committee Name</th><th>Description</th></tr></thead><tbody>';
        for (const comm of committees) {
            html += `
                <tr>
                    <td>${comm.id}</td>
                    <td>${comm.event_name || 'N/A'}</td>
                    <td>${comm.name}</td>
                    <td>${comm.description || 'N/A'}</td>
                </tr>
            `;
        }
        html += '</tbody></table>';
        container.innerHTML = html;
    } catch (error) {
        displayMessage('error', 'Failed to load committees: ' + error.message);
        container.innerHTML = '<p>Error loading committees.</p>';
        console.error("Error loading my committees:", error); // Debugging log
    }
});

// --- My Announcements ---
navButtons.myAnnouncements.addEventListener('click', async () => {
    console.log("Navigating to My Announcements."); // Debugging log
    showSection('my-announcements-section');
    const container = document.getElementById('my-announcements-list');
    container.innerHTML = 'Loading announcements...';

    try {
        const announcements = await apiClient.volunteers.myAnnouncements();
        if (announcements.length === 0) {
            container.innerHTML = '<p>No announcements relevant to you.</p>';
            return;
        }
        let html = '<table><thead><tr><th>Title</th><th>Priority</th><th>Body</th><th>Expires</th><th>Committee</th><th>Created By</th></tr></thead><tbody>';
        for (const ann of announcements) {
            html += `
                <tr>
                    <td>${ann.title}</td>
                    <td>${ann.priority}</td>
                    <td>${ann.body}</td>
                    <td>${ann.expires_at ? new Date(ann.expires_at).toLocaleString() : 'Never'}</td>
                    <td>${ann.committee_name || 'Event-wide'}</td>
                    <td>${ann.created_by_name || 'N/A'}</td>
                </tr>
            `;
        }
        html += '</tbody></table>';
        container.innerHTML = html;
    } catch (error) {
        displayMessage('error', 'Failed to load announcements: ' + error.message);
        container.innerHTML = '<p>Error loading announcements.</p>';
        console.error("Error loading my announcements:", error); // Debugging log
    }
});

// --- Ask a Question ---
navButtons.askQuestion.addEventListener('click', () => {
    console.log("Navigating to Ask a Question section."); // Debugging log
    showSection('ask-question-section');
    document.getElementById('ask-question-form').reset();
});

document.getElementById('ask-question-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const questionText = document.getElementById('question-text').value;
    const eventId = document.getElementById('question-event-id').value;
    const committeeId = document.getElementById('question-committee-id').value;

    try {
        await apiClient.questions.ask(questionText, eventId || undefined, committeeId || undefined);
        displayMessage('success', 'Question submitted successfully!');
        document.getElementById('ask-question-form').reset();
        navButtons.myQuestions.click(); // Show my questions after asking
    } catch (error) {
        displayMessage('error', 'Failed to submit question: ' + error.message);
        console.error("Error submitting question:", error); // Debugging log
    }
});

// --- My Questions ---
navButtons.myQuestions.addEventListener('click', async () => {
    console.log("Navigating to My Questions."); // Debugging log
    showSection('my-questions-section');
    const container = document.getElementById('my-questions-list');
    container.innerHTML = 'Loading your questions...';

    try {
        const questions = await apiClient.questions.myQuestions();
        if (questions.length === 0) {
            container.innerHTML = '<p>You have not asked any questions yet.</p>';
            return;
        }
        renderQuestionsTable(container, questions, false); // Not admin view, no answer forms
    } catch (error) {
        displayMessage('error', 'Failed to load your questions: ' + error.message);
        container.innerHTML = '<p>Error loading questions.</p>';
        console.error("Error loading my questions:", error); // Debugging log
    }
});


// --- Admin Dashboard Event Listeners ---
navButtons.adminDashboard.addEventListener('click', async () => {
    console.log("Admin Dashboard button clicked."); // Debugging log
    await showAdminDashboard();
});

async function loadAdminDashboardData() {
    console.log("Loading admin dashboard data..."); // Debugging log
    await loadAllVolunteers();
    await loadAllAssignments();
    await loadPendingQuestions();
    console.log("Finished loading admin dashboard data."); // Debugging log
}

// Admin: All Volunteers
async function loadAllVolunteers() {
    const container = document.getElementById('all-volunteers-list');
    container.innerHTML = 'Loading all volunteers...';
    try {
        const volunteers = await apiClient.volunteers.listAll();
        if (volunteers.length === 0) {
            container.innerHTML = '<p>No volunteers registered.</p>';
            return;
        }
        renderVolunteersTable(container, volunteers);
    } catch (error) {
        displayMessage('error', 'Failed to load all volunteers: ' + error.message);
        container.innerHTML = '<p>Error loading volunteers.</p>';
        console.error("Error loading all volunteers:", error); // Debugging log
    }
}

function renderVolunteersTable(container, volunteers) {
    let html = `
        <table>
            <thead>
                <tr>
                    <th>ID</th>
                    <th>Name</th>
                    <th>Email</th>
                    <th>Phone</th>
                    <th>Dept</th>
                    <th>College ID</th>
                    <th>Created At</th>
                </tr>
            </thead>
            <tbody>
    `;
    for (const vol of volunteers) {
        html += `
            <tr>
                <td>${vol.id}</td>
                <td>${vol.name}</td>
                <td>${vol.email || 'N/A'}</td>
                <td>${vol.phone || 'N/A'}</td>
                <td>${vol.dept || 'N/A'}</td>
                <td>${vol.college_id || 'N/A'}</td>
                <td>${new Date(vol.created_at).toLocaleDateString()}</td>
            </tr>
        `;
    }
    html += `</tbody></table>`;
    container.innerHTML = html;
}

// Admin: All Assignments
async function loadAllAssignments() {
    const container = document.getElementById('all-assignments-list');
    container.innerHTML = 'Loading all assignments...';
    try {
        const assignments = await apiClient.volunteers.listAllAssignments();
        if (assignments.length === 0) {
            container.innerHTML = '<p>No assignments found.</p>';
            return;
        }
        renderAssignmentsTable(container, assignments, false); // false for admin view (no check-in/out)
    } catch (error) {
        displayMessage('error', 'Failed to load all assignments: ' + error.message);
        container.innerHTML = '<p>Error loading assignments.</p>';
        console.error("Error loading all assignments:", error); // Debugging log
    }
}

// Admin: Pending Questions
async function loadPendingQuestions() {
    const container = document.getElementById('pending-questions-list');
    container.innerHTML = 'Loading pending questions...';
    try {
        const questions = await apiClient.questions.listPending();
        if (questions.length === 0) {
            container.innerHTML = '<p>No pending questions.</p>';
            return;
        }
        renderQuestionsTable(container, questions, true); // true for admin view (with answer forms)
    } catch (error) {
        displayMessage('error', 'Failed to load pending questions: ' + error.message);
        container.innerHTML = '<p>Error loading questions.</p>';
        console.error("Error loading pending questions:", error); // Debugging log
    }
}

function renderQuestionsTable(container, questions, isAdminView = false) {
    let html = `
        <table>
            <thead>
                <tr>
                    <th>ID</th>
                    ${isAdminView ? '<th>Volunteer</th>' : ''}
                    <th>Question</th>
                    <th>Asked At</th>
                    <th>Status</th>
                    <th>Answer</th>
                    ${isAdminView ? '<th>Actions</th>' : ''}
                </tr>
            </thead>
            <tbody>
    `;
    for (const q of questions) {
        const statusClass = q.answer_text ? 'answered' : 'pending';
        const statusText = q.answer_text ? 'Answered' : 'Pending';
        html += `
            <tr>
                <td>${q.id}</td>
                ${isAdminView ? `<td>${q.volunteer_name || 'N/A'} (${q.volunteer_id || 'N/A'})</td>` : ''}
                <td>${q.question_text}</td>
                <td>${new Date(q.asked_at).toLocaleString()}</td>
                <td><span class="question-status ${statusClass}">${statusText}</span></td>
                <td>
                    ${q.answer_text ? `<strong>By ${q.answered_by_name || 'N/A'}:</strong> ${q.answer_text} <br> <em>(${new Date(q.answered_at).toLocaleString()})</em>` : 'Not yet answered'}
                </td>
                ${isAdminView && !q.answer_text ? `
                    <td>
                        <button class="answer-button" data-question-id="${q.id}">Answer</button>
                    </td>
                ` : isAdminView ? '<td></td>' : ''}
            </tr>
            ${isAdminView && !q.answer_text ? `
                <tr id="answer-form-row-${q.id}" class="hidden">
                    <td colspan="${isAdminView ? 7 : 5}">
                        <div class="answer-form-container">
                            <form class="answer-form" data-question-id="${q.id}">
                                <label for="answer-text-${q.id}">Your Answer:</label>
                                <textarea id="answer-text-${q.id}" rows="3" required></textarea>
                                <button type="submit">Submit Answer</button>
                                <button type="button" class="cancel-answer-button" data-question-id="${q.id}">Cancel</button>
                            </form>
                        </div>
                    </td>
                </tr>
            ` : ''}
        `;
    }
    html += `</tbody></table>`;
    container.innerHTML = html;

    if (isAdminView) {
        container.querySelectorAll('.answer-button').forEach(button => {
            button.addEventListener('click', () => {
                const questionId = button.dataset.questionId;
                document.getElementById(`answer-form-row-${questionId}`).classList.toggle('hidden');
            });
        });

        container.querySelectorAll('.cancel-answer-button').forEach(button => {
            button.addEventListener('click', () => {
                const questionId = button.dataset.questionId;
                document.getElementById(`answer-form-row-${questionId}`).classList.add('hidden');
                document.getElementById(`answer-text-${questionId}`).value = ''; // Clear textarea
            });
        });

        container.querySelectorAll('.answer-form').forEach(form => {
            form.addEventListener('submit', async (e) => {
                e.preventDefault();
                const questionId = parseInt(form.dataset.questionId);
                const answerText = form.querySelector('textarea').value;

                try {
                    await apiClient.questions.answer(questionId, answerText);
                    displayMessage('success', 'Question answered successfully!');
                    console.log(`Question ${questionId} answered.`); // Debugging log
                    loadPendingQuestions(); // Reload pending questions
                } catch (error) {
                    displayMessage('error', 'Failed to answer question: ' + error.message);
                    console.error(`Error answering question ${questionId}:`, error); // Debugging log
                }
            });
        });
    }
}


// Admin: Create Volunteer
document.getElementById('show-create-volunteer-form').addEventListener('click', () => {
    document.getElementById('create-volunteer-form-container').classList.toggle('hidden');
});

document.getElementById('create-volunteer-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const data = {
        name: document.getElementById('admin-vol-name').value,
        email: document.getElementById('admin-vol-email').value || undefined,
        phone: document.getElementById('admin-vol-phone').value || undefined,
        dept: document.getElementById('admin-vol-dept').value || undefined,
        college_id: document.getElementById('admin-vol-college-id').value || undefined,
        password: document.getElementById('admin-vol-password').value || undefined,
    };
    console.log("Attempting to create volunteer (Admin)."); // Debugging log

    try {
        const result = await apiClient.volunteers.create(data);
        displayMessage('success', `Volunteer created: ${result.name} (ID: ${result.id})`);
        document.getElementById('create-volunteer-form').reset();
        document.getElementById('create-volunteer-form-container').classList.add('hidden');
        loadAllVolunteers(); // Reload list
    } catch (error) {
        displayMessage('error', 'Failed to create volunteer: ' + error.message);
        console.error("Error creating volunteer (Admin):", error); // Debugging log
    }
});

// Admin: Bulk Upload
document.getElementById('show-bulk-upload-form').addEventListener('click', () => {
    document.getElementById('bulk-upload-form-container').classList.toggle('hidden');
});

document.getElementById('bulk-upload-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const eventId = document.getElementById('bulk-event-id').value;
    const committeeId = document.getElementById('bulk-committee-id').value;
    const fileInput = document.getElementById('bulk-csv-file');

    if (!eventId || !committeeId || !fileInput.files.length) {
        displayMessage('error', 'Event ID, Committee ID, and a CSV file are required.');
        return;
    }

    const formData = new FormData();
    formData.append('file', fileInput.files[0]);
    console.log("Attempting bulk upload."); // Debugging log

    try {
        const result = await apiClient.volunteers.bulkUpload(eventId, committeeId, formData);
        let msg = `Bulk upload complete. Created: ${result.created_volunteers} volunteers, ${result.created_assignments} assignments.`;
        if (result.errors && result.errors.length > 0) {
            msg += ` Errors on ${result.errors.length} rows. See console for details.`;
            console.error("Bulk Upload Errors:", result.errors);
            displayMessage('error', msg);
        } else {
            displayMessage('success', msg);
        }
        document.getElementById('bulk-upload-form').reset();
        document.getElementById('bulk-upload-form-container').classList.add('hidden');
        loadAdminDashboardData(); // Reload all admin lists
    } catch (error) {
        displayMessage('error', 'Bulk upload failed: ' + error.message);
        console.error("Error during bulk upload:", error); // Debugging log
    }
});

// Admin: Export Volunteers CSV
document.getElementById('export-volunteers-csv').addEventListener('click', async () => {
    console.log("Initiating volunteers CSV export."); // Debugging log
    try {
        const response = await fetch(`${API_BASE_URL}/volunteers/export_csv`, {
            headers: { 'Authorization': `Bearer ${accessToken}` }
        });
        if (!response.ok) {
            const errorText = await response.text();
            throw new Error(errorText || `API Error: ${response.statusText}`);
        }

        const blob = await response.blob();
        const url = window.URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = 'volunteers_export.csv';
        document.body.appendChild(a);
        a.click();
        a.remove();
        window.URL.revokeObjectURL(url);
        displayMessage('success', 'Volunteers CSV export started.');
    } catch (error) {
        displayMessage('error', 'Failed to export volunteers: ' + error.message);
        console.error("Error exporting volunteers CSV:", error); // Debugging log
    }
});

// Admin: Export Assignments CSV
document.getElementById('export-assignments-csv').addEventListener('click', async () => {
    console.log("Initiating assignments CSV export."); // Debugging log
    try {
        const response = await fetch(`${API_BASE_URL}/volunteers/assignments/export_csv`, {
            headers: { 'Authorization': `Bearer ${accessToken}` }
        });
        if (!response.ok) {
            const errorText = await response.text();
            throw new Error(errorText || `API Error: ${response.statusText}`);
        }

        const blob = await response.blob();
        const url = window.URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = 'volunteer_assignments_export.csv';
        document.body.appendChild(a);
        a.click();
        a.remove();
        window.URL.revokeObjectURL(url);
        displayMessage('success', 'Assignments CSV export started.');
    } catch (error) {
        displayMessage('error', 'Failed to export assignments: ' + error.message);
        console.error("Error exporting assignments CSV:", error); // Debugging log
    }
});


// --- Initial Load ---
document.addEventListener('DOMContentLoaded', () => {
    accessToken = localStorage.getItem('accessToken');
    currentUser = localStorage.getItem('currentUser');
    currentRole = localStorage.getItem('currentRole');
    console.log("DOM loaded. Restoring session:", { accessToken: !!accessToken, currentUser, currentRole }); // Debugging log

    // Attach navigation event listeners for role-specific buttons
    navButtons.home.addEventListener('click', renderApp);
    navButtons.myAssignments.addEventListener('click', () => navButtons.myAssignments.click()); // Force click for initial load logic
    navButtons.myCommittees.addEventListener('click', () => navButtons.myCommittees.click());
    navButtons.myAnnouncements.addEventListener('click', () => navButtons.myAnnouncements.click());
    navButtons.myQuestions.addEventListener('click', () => navButtons.myQuestions.click());
    navButtons.askQuestion.addEventListener('click', () => navButtons.askQuestion.click());
    // Admin dashboard navigation is already handled by its event listener
});

// Initial render
renderApp();