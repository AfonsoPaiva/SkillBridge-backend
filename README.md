<div align="center">

# SkillBridge — Backend

**REST API powering [SkillBridge](https://skillbridge.pt/landing)**, the free non-profit platform that connects Portuguese university students to real collaborative projects, recruiters, and job opportunities.

[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![Gin](https://img.shields.io/badge/Framework-Gin-00ADD8)](https://gin-gonic.com)
[![CockroachDB](https://img.shields.io/badge/Database-CockroachDB-6933FF?logo=cockroachlabs&logoColor=white)](https://www.cockroachlabs.com)
[![Docker](https://img.shields.io/badge/Container-Docker-2496ED?logo=docker&logoColor=white)](https://www.docker.com)
[![GCP](https://img.shields.io/badge/Deployed%20on-Google%20Cloud-4285F4?logo=googlecloud&logoColor=white)](https://cloud.google.com)

</div>

---

## About

SkillBridge tackles Portugal's youth-employment paradox — graduates struggling to find their first opportunity while companies struggle to find early-career talent — by giving students a place to build real projects together and get discovered by recruiters. This repository is the Go backend that powers the platform: authentication, projects, job vacancies, recruiter tooling, messaging, donations, and the admin back office.

The frontend (Angular) consumes this API; it lives in a separate repository.

## Features

**Students & Users**
- Firebase-authenticated accounts, public profiles, and a guest-session flow so visitors can start onboarding before creating an account
- Skill tagging against a curated, embedded skills catalog
- Follow / unfollow other users, with follower/following counts
- Reviews between users, plus a moderated university/course review system

**Projects**
- Create, update, and manage collaborative projects with custom roles
- Join requests and applications, with owner approval/rejection
- Multiple project owners and member management

**Jobs & Vacancies**
- Recruiter self-service: apply to become a recruiter, magic-link login (no password), post/edit/delete vacancies
- Public job board with favorites, applications, and application-status tracking
- Automated **LinkedIn job scraper** and a **multi-source scraper**, both run on a schedule
- Automatic vacancy expiry and a one-week application follow-up job
- Community-facing stats (e.g. total vacancies, applications) for social proof

**Messaging & Notifications**
- End-to-end encrypted private messaging (public-key exchange, conversations, unread counts)
- Push notifications via Firebase Cloud Messaging

**Donations**
- Stripe embedded checkout and webhook handling for platform donations, with public stats

**Admin Back Office**
- Static admin dashboard, gated behind an IP allowlist
- User, project, review, donation, and recruiter management
- TOTP (2FA) required for sensitive admin actions, on top of a Firebase-UID allowlist
- Audit log of admin actions, marketing email tool, and image cleanup utility

## Tech Stack

| Layer | Technology |
|---|---|
| Language | Go 1.25 |
| Web framework | [Gin](https://gin-gonic.com) |
| Database | CockroachDB (via [GORM](https://gorm.io) + `pgx`) |
| Auth | Firebase Authentication |
| File storage | Google Cloud Storage |
| Email | [Resend](https://resend.com) |
| Payments | [Stripe](https://stripe.com) |
| 2FA | TOTP ([pquerna/otp](https://github.com/pquerna/otp)) |
| Bot protection | Google reCAPTCHA v3 |
| API docs | Swagger ([swaggo/swag](https://github.com/swaggo/swag)) |
| Image processing | [disintegration/imaging](https://github.com/disintegration/imaging) |
| Job scraping | [goquery](https://github.com/PuerkitoBio/goquery) |
| Containerization | Docker, multi-stage build |
| CI/CD & hosting | Google Cloud Build + Cloud Run/Scheduler |

## Security

The API is built with a layered security model, applied globally before any route runs:

- Hardened response headers (HSTS, CSP, anti-clickjacking)
- Global rate limiting (300 req/min per IP) plus dedicated limiters for sensitive writes, heavy reads, auth flows, donations, and the image proxy
- Bot/scanner user-agent filtering and SQL-injection / XSS pattern blocking on paths and query strings
- Request body size caps (512 KB default, 10 MB for image uploads)
- Strict, allowlist-based CORS (with support for Vercel preview deployments)
- reCAPTCHA v3 verification on high-risk public endpoints (password reset, recruiter signup, guest sessions, donations)
- Admin routes protected by Firebase UID allowlist, optional IP allowlist, and mandatory TOTP 2FA
- Internal cron endpoints authenticated via a shared secret header, not Firebase

See [`SECURITY.md`](./SECURITY.md) for the vulnerability disclosure policy.

## Project Structure

```
.
├── cmd/server/           # Application entrypoint (main.go)
├── config/               # Env config loader + embedded static data
│   ├── config.go         #   (skills.json, UniversidadesECursos.json)
│   ├── skills.json
│   └── UniversidadesECursos.json
├── docs/                 # Auto-generated Swagger docs
├── internal/
│   ├── audit/            # Admin action audit logging
│   ├── database/         # DB connection + migrations
│   ├── email/            # Resend integration + email rate limiting
│   ├── handlers/         # HTTP handlers, one file per domain
│   ├── jobs/             # Cron jobs (scrapers, vacancy expiry/follow-up)
│   ├── middleware/       # Auth, admin, security, rate limiting, reCAPTCHA, TOTP
│   ├── models/           # GORM data models
│   ├── notifications/    # Push notifications (FCM)
│   ├── routes/           # Route registration
│   └── storage/          # Google Cloud Storage integration
├── scripts/              # Cloud Scheduler setup script
├── admin-dashboard.html  # Static admin panel (served behind IP allowlist)
├── Dockerfile
└── cloudbuild.yaml       # Google Cloud Build pipeline
```

## API Overview

All endpoints are namespaced under `/api`. A machine-readable summary is available at runtime via `GET /api`, and full Swagger docs are served at `/swagger/index.html` outside production.

| Group | Auth | Examples |
|---|---|---|
| Health / Info | none | `GET /health`, `GET /api` |
| Users | Firebase | `POST /users/register`, `GET /users/me`, `POST /users/:id/follow` |
| Projects | Firebase / public | `POST /projects`, `GET /projects`, `POST /projects/:id/join` |
| Vacancies | Firebase / public | `GET /vacancies`, `POST /vacancies/:id/apply` |
| Recruiters | Firebase / public | `POST /recruiters/apply`, `POST /recruiter/vacancies` |
| Messaging | Firebase | `POST /conversations`, `GET /conversations/:id/messages` |
| Universities | public | `GET /universities`, `POST /universities/reviews` |
| Donations | public | `POST /donations/embedded-checkout`, `POST /donations/webhook` |
| Admin | Firebase + UID + TOTP | `GET /admin/users`, `GET /admin/audit-logs` |
| Internal | shared secret | `POST /internal/scrape-jobs` |

## Getting Started

### Prerequisites

- Go 1.25+
- A CockroachDB (or Postgres-compatible) instance
- A Firebase project (Authentication + Cloud Messaging)
- A Google Cloud Storage bucket (optional — needed for image uploads)
- API keys for Resend, Stripe, and reCAPTCHA v3 (optional, feature-dependent)

### Configuration

Copy your secrets into a `.env` file at the project root (loaded automatically on startup). Key variables:

```env
# Server
PORT=8080
ENV=development
FRONTEND_URL=http://localhost:4200
ALLOWED_ORIGINS=http://localhost:4200

# Database
DATABASE_URL=postgresql://user:pass@host:26257/skillbridge?sslmode=verify-full

# Firebase
FIREBASE_CREDENTIALS_PATH=./config/firebase-credentials.json
FIREBASE_API_KEY=
FIREBASE_AUTH_DOMAIN=
FIREBASE_PROJECT_ID=

# Google Cloud Storage
GCS_BUCKET_NAME=
GCS_PROJECT_ID=

# Email (Resend)
RESEND_API_KEY=
EMAIL_FROM_ADDRESS=noreply@skillbridge.pt

# Stripe
STRIPE_SECRET_KEY=
STRIPE_WEBHOOK_SECRET=

# reCAPTCHA v3
RECAPTCHA_SECRET_KEY=
RECAPTCHA_SITE_KEY=

# Admin access
ADMIN_UIDS=uid1,uid2
ADMIN_ALLOWED_IPS=
```

### Run locally

```bash
git clone https://github.com/AfonsoPaiva/SkillBridge-backend.git
cd SkillBridge-backend
go mod download
go run ./cmd/server
```

The API will be available at `http://localhost:8080`, with a health check at `/health`.

### Run with Docker

```bash
docker build -t skillbridge-backend .
docker run -p 8080:8080 --env-file .env skillbridge-backend
```

### Deployment

The included `cloudbuild.yaml` builds and deploys the container via Google Cloud Build. `scripts/setup-cloud-scheduler.sh` configures the Cloud Scheduler job that triggers `/api/internal/scrape-jobs` for the vacancy scraper.

## Author

Built and maintained by **[Afonso Paiva](https://github.com/AfonsoPaiva)**, founder of [SkillBridge](https://skillbridge.pt).
