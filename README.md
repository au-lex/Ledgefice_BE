# VMS Backend — Go Fiber + PostgreSQL

Voucher Management System backend for Zenith Construction.

## Stack
- **Go 1.22** + **Fiber v2** (HTTP)
- **GORM** (ORM) + **PostgreSQL 16**
- **JWT** (HS256) for auth
- **bcrypt** for password hashing

---

## Quick Start

```bash
# 1. Start Postgres
docker compose up -d

# 2. Copy env
cp .env.example .env   # edit DB_PASSWORD, JWT_SECRET

# 3. Install deps
go mod tidy

# 4. Run (auto-migrates on startup)
make run
```

---

## Project Structure

```
cmd/server/main.go          Entry point
internal/
  config/     Config loader (env vars + .env)
  database/   GORM connect + AutoMigrate
  middleware/ JWT guard, CORS, logger
  models/     All domain models
  handlers/   HTTP handlers + route registration
  services/   Business logic (approval, audit, duplicate)
pkg/utils/    JWT generation, pagination, response helpers
```

---

## API Reference

### Auth
| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/auth/register` | Create account |
| POST | `/api/v1/auth/login` | Login → JWT |
| GET  | `/api/v1/auth/me` | Current user (🔒) |

### Users (🔒)
| Method | Path | Description |
|--------|------|-------------|
| GET  | `/api/v1/users` | List users (`?search=`, `?status=`, `?department=`) |
| GET  | `/api/v1/users/:id` | Get user |
| PUT  | `/api/v1/users/:id` | Update user |
| DELETE | `/api/v1/users/:id` | Delete user |
| PATCH | `/api/v1/users/:id/status` | Set status (`active`/`suspended`/`blocked`) |

### Departments (🔒)
| Method | Path | Description |
|--------|------|-------------|
| GET  | `/api/v1/departments` | List (includes active_vouchers, total_spend) |
| GET  | `/api/v1/departments/:id` | Get |
| POST | `/api/v1/departments` | Create |
| PUT  | `/api/v1/departments/:id` | Update |
| DELETE | `/api/v1/departments/:id` | Delete |

### Voucher Types (🔒)
| Method | Path | Description |
|--------|------|-------------|
| GET  | `/api/v1/voucher-types` | List with custom fields |
| GET  | `/api/v1/voucher-types/:id` | Get |
| POST | `/api/v1/voucher-types` | Create |
| PUT  | `/api/v1/voucher-types/:id` | Update (replaces fields) |
| DELETE | `/api/v1/voucher-types/:id` | Delete |

### Approval Chains (🔒)
| Method | Path | Description |
|--------|------|-------------|
| GET  | `/api/v1/approval-chains` | List with tiers + steps |
| GET  | `/api/v1/approval-chains/:id` | Get |
| POST | `/api/v1/approval-chains` | Create |
| PUT  | `/api/v1/approval-chains/:id` | Update (replaces tiers) |
| DELETE | `/api/v1/approval-chains/:id` | Delete |

### Vouchers (🔒)
| Method | Path | Description |
|--------|------|-------------|
| GET  | `/api/v1/vouchers` | List (`?status=`, `?type=`, `?department=`, `?search=`, `?sort=newest/oldest/highest/lowest`) |
| GET  | `/api/v1/vouchers/:id` | Get with full history |
| POST | `/api/v1/vouchers` | Create (draft) |
| DELETE | `/api/v1/vouchers/:id` | Delete draft only |
| POST | `/api/v1/vouchers/:id/submit` | Move draft → pending |
| POST | `/api/v1/vouchers/:id/approve` | Approve step `{role, comment}` |
| POST | `/api/v1/vouchers/:id/reject` | Reject `{role, reason}` |
| DELETE | `/api/v1/vouchers/:id/duplicate-flag` | Dismiss duplicate flag |

### Reports (🔒)
| Method | Path | Description |
|--------|------|-------------|
| GET  | `/api/v1/reports/summary` | KPI totals (`?range=7d/30d/90d/12m`) |
| GET  | `/api/v1/reports/spend-over-time` | Time series |
| GET  | `/api/v1/reports/spend-by-dept` | Dept breakdown |
| GET  | `/api/v1/reports/volume-by-type` | Count + value per type |

### Audit Log (🔒)
| Method | Path | Description |
|--------|------|-------------|
| GET  | `/api/v1/audit-logs` | Paginated log (`?module=`, `?action=`, `?search=`) |

---

## Request/Response Convention

All protected routes require:
```
Authorization: Bearer <token>
```

Success responses:
```json
{ "data": <payload> }
```

Paginated responses:
```json
{
  "data": [...],
  "page": 1,
  "limit": 20,
  "total_items": 100,
  "total_pages": 5
}
```

Error responses:
```json
{ "error": "message" }
```

---

## Key Business Rules Implemented

1. **Voucher lifecycle**: `draft → pending → approved/rejected`
2. **Approval chain resolution**: amount tier lookup selects the right sequence of approver roles
3. **Duplicate detection**: flags same vendor+amount within 30 days, or matching invoice ref
4. **Audit trail**: every mutating action writes an immutable `AuditLog` entry
5. **Department permissions**: stored as JSON array on the department row; permission guard hook ready to plug in

---

## Notes / TODOs

- `VoucherCode()` uses a random suffix — swap for a Postgres sequence for guaranteed uniqueness
- File upload for voucher attachments: add an S3/local storage handler and `VoucherAttachment` model
- Billing/subscription endpoints are UI-only in the frontend; add Paystack or Flutterwave webhook handler when ready
- Settings (profile, org, notifications) need their own handler/model when personalised data is required
- Notifications (email on approval) hook — add a mailer service call inside `AdvanceApproval`
