# Flyway Database Migrations with GoBricks

This guide demonstrates how to use **Flyway** for database migrations in a GoBricks application, with support for both **PostgreSQL** and **Oracle** databases.

## 📋 Table of Contents

1. [Overview](#overview)
2. [Quick Start](#quick-start)
3. [Migration Structure](#migration-structure)
4. [Running Migrations](#running-migrations)
5. [Migration Module](#migration-module)
6. [Testing Scenarios](#testing-scenarios)
7. [Troubleshooting](#troubleshooting)

---

## 🎯 Overview

This demo showcases:

- ✅ **Flyway integration** with GoBricks framework
- ✅ **Multi-database support** (PostgreSQL & Oracle)
- ✅ **Versioned migrations** with V1, V2, V3 examples
- ✅ **Automatic migration execution** on app startup (environment-aware)
- ✅ **Docker-based migrations** (no local Flyway installation required)
- ✅ **Manual migration control** via HTTP API endpoints

## 🚀 Quick Start

### Option 1: Docker-Based (Recommended for Testing)

No local Flyway installation needed!

```bash
# 1. Start databases
make docker-up

# 2. Run PostgreSQL migrations
make migrate-run-docker

# 3. Check migration status
make migrate-info-docker

# 4. Run Oracle migrations
make migrate-run-oracle-docker

# 5. Verify all databases
make migrate-verify-all
```

### Option 2: Local Flyway Installation

```bash
# Install Flyway CLI
brew install flyway  # macOS
# or download from https://flywaydb.org/download/

# Run migrations
make migrate-full  # PostgreSQL: info -> validate -> migrate
make migrate-run-oracle  # Oracle migrations
```

### Option 3: Application-Driven (Production Pattern)

```bash
# The application automatically runs migrations on startup
# based on environment configuration

# Development: Executes migrations automatically
APP_ENV=development make run

# Production: Only validates migrations (safety-first)
APP_ENV=production make run
```

---

## 📁 Migration Structure

```
go-bricks-demo-project/
├── flyway/
│   ├── flyway-postgresql.conf    # PostgreSQL Flyway config
│   └── flyway-oracle.conf         # Oracle Flyway config
├── migrations/
│   ├── postgresql/
│   │   ├── V1__create_schema_baseline.sql
│   │   ├── V2__add_projects_and_tasks.sql
│   │   └── V3__add_audit_logging.sql
│   └── oracle/
│       ├── V1__create_schema_baseline.sql
│       ├── V2__add_projects_and_tasks.sql
│       └── V3__add_audit_logging.sql
└── internal/modules/migrations/
    └── module.go                  # GoBricks migration module
```

### Migration File Naming Convention

Flyway follows a strict naming pattern:

```
V{version}__{description}.sql
```

Examples:
- `V1__create_schema_baseline.sql` - Initial schema
- `V2__add_projects_and_tasks.sql` - Feature addition
- `V3__add_audit_logging.sql` - Audit system
- `V4__fix_user_constraints.sql` - Bug fix

**Rules:**
- Version must be numeric and unique
- Double underscore `__` separates version from description
- Description uses underscores for spaces
- File must end with `.sql`

---

## 🔧 Migration Files Overview

### V1: Schema Baseline

**Creates:**
- `users` table with authentication fields
- `departments` table with hierarchy
- Indexes for performance
- Seed data for initial users and departments
- Auto-update triggers for `updated_at` fields

**Key Features:**
- Email validation constraints
- Cascade delete relationships
- Automatic timestamp management

### V2: Projects and Tasks

**Creates:**
- `projects` table for project management
- `tasks` table for task tracking
- `project_members` junction table (many-to-many)
- Business logic constraints (status, priority)
- Sample project and task data

**Key Features:**
- Status workflow constraints
- Budget validation
- Date range validation
- Multi-user project assignments

### V3: Audit Logging

**Creates:**
- `audit_log` table for compliance
- Automatic audit triggers for critical tables
- JSON-based change tracking
- Views for audit analysis

**Key Features:**
- Captures INSERT, UPDATE, DELETE operations
- Stores before/after snapshots
- Selective logging (only significant changes)
- Performance-optimized indexes

---

## 🎮 Running Migrations

### Make Targets Reference

#### PostgreSQL Migrations (Local Flyway)

```bash
make migrate-info         # Show migration status
make migrate-validate     # Validate migration files
make migrate-run          # Execute pending migrations
make migrate-full         # Full workflow: info -> validate -> migrate
make migrate-clean        # ⚠️ DESTRUCTIVE: Drop all objects
make migrate-repair       # Fix migration history issues
```

#### PostgreSQL Migrations (Docker)

```bash
make migrate-info-docker      # Show migration status
make migrate-validate-docker  # Validate migration files
make migrate-run-docker       # Execute pending migrations
make migrate-full-docker      # Full workflow via Docker
```

#### Oracle Migrations

```bash
make migrate-info-oracle        # Local: Show Oracle status
make migrate-run-oracle         # Local: Run Oracle migrations
make migrate-info-oracle-docker # Docker: Show Oracle status
make migrate-run-oracle-docker  # Docker: Run Oracle migrations
```

#### Verification

```bash
make migrate-verify-all  # Check status in both PostgreSQL and Oracle
```

### Manual Flyway Commands

If you prefer direct Flyway usage:

```bash
# PostgreSQL
flyway -configFiles=flyway/flyway-postgresql.conf \
       -locations=filesystem:migrations/postgresql \
       info

flyway -configFiles=flyway/flyway-postgresql.conf \
       -locations=filesystem:migrations/postgresql \
       migrate

# Oracle
flyway -configFiles=flyway/flyway-oracle.conf \
       -locations=filesystem:migrations/oracle \
       migrate
```

### Docker Compose Commands

```bash
# PostgreSQL - Info
docker-compose run --rm flyway-postgres info

# PostgreSQL - Migrate
docker-compose run --rm flyway-postgres migrate

# Oracle - Info
docker-compose run --rm flyway-oracle info

# Oracle - Migrate
docker-compose run --rm flyway-oracle migrate

# Override command
docker-compose run --rm flyway-postgres validate
docker-compose run --rm flyway-postgres repair
```

---

## 🧩 Migration Module Integration

The `migrations` module demonstrates GoBricks module integration:

### Module Features

**Automatic Startup Migrations:**
```go
// Development: Runs migrations automatically
// Production: Validates only (safety-first)
func (m *Module) Init(deps *app.ModuleDeps) error {
    m.migrator = migration.NewFlywayMigrator(deps.Config, m.logger)
    return m.migrator.RunMigrationsAtStartup(ctx)
}
```

**HTTP API Endpoints:**

```bash
# GET /api/v1/migrations/info
# Get migration status information
curl http://localhost:8080/api/v1/migrations/info

# GET /api/v1/migrations/validate
# Validate pending migrations
curl http://localhost:8080/api/v1/migrations/validate

# POST /api/v1/migrations/run
# Execute pending migrations (⚠️ should be protected in production)
curl -X POST http://localhost:8080/api/v1/migrations/run
```

### Enabling the Migration Module

Update `cmd/api/main.go`:

```go
import (
    "github.com/gaborage/go-bricks-demo-project/internal/modules/migrations"
)

func getModulesToLoad() []ModuleConfig {
    return []ModuleConfig{
        {
            Name:    "migrations",
            Enabled: true,  // Enable migration module
            Module:  migrations.NewModule(),
        },
        {
            Name:    "products",
            Enabled: true,
            Module:  products.NewModule(),
        },
    }
}
```

---

## 🧪 Testing Scenarios

### Scenario 1: Fresh Database Setup

```bash
# Start clean databases
make docker-down
make docker-up

# Run all migrations
make migrate-run-docker
make migrate-run-oracle-docker

# Verify schema
docker exec -it postgres-default psql -U postgres -d postgres -c "\dt"
```

Expected output:
```
                List of relations
 Schema |         Name         | Type  |  Owner
--------+----------------------+-------+----------
 public | audit_log            | table | postgres
 public | departments          | table | postgres
 public | flyway_schema_history| table | postgres
 public | project_members      | table | postgres
 public | projects             | table | postgres
 public | tasks                | table | postgres
 public | users                | table | postgres
```

### Scenario 2: Incremental Migration

```bash
# Check current status
make migrate-info-docker

# Output shows V1, V2, V3 applied
# Create new migration
cat > migrations/postgresql/V4__add_notifications.sql << 'EOF'
CREATE TABLE notifications (
    id SERIAL PRIMARY KEY,
    user_id INTEGER REFERENCES users(id),
    message TEXT,
    read BOOLEAN DEFAULT false,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
EOF

# Run incremental migration
make migrate-run-docker

# Verify - only V4 executes
```

### Scenario 3: Environment-Based Behavior

```bash
# Development - Auto-migration
APP_ENV=development make run
# Logs show: "Running automatic migrations in development environment"

# Production - Validation only
APP_ENV=production make run
# Logs show: "Validating migrations in non-development environment"
```

### Scenario 4: Migration Validation

```bash
# Validate before executing
make migrate-validate-docker

# Intentionally break a migration
echo "INVALID SQL" >> migrations/postgresql/V4__broken.sql

# Validation should fail
make migrate-validate-docker
# Error: Migration validation failed

# Fix or remove the broken migration
rm migrations/postgresql/V4__broken.sql
```

### Scenario 5: Multi-Database Verification

```bash
# Check all databases at once
make migrate-verify-all

# Output shows:
# === PostgreSQL Migration Status ===
# V1, V2, V3 applied
#
# === Oracle Migration Status ===
# V1, V2, V3 applied
```

### Scenario 6: Audit Log Testing

```bash
# Run migrations to get audit logging
make migrate-run-docker

# Make changes to trigger audit logs
docker exec -it postgres-default psql -U postgres -d postgres << 'EOF'
-- Insert triggers audit
INSERT INTO users (username, email, full_name)
VALUES ('test.user', 'test@example.com', 'Test User');

-- Update triggers audit
UPDATE users SET full_name = 'Updated User' WHERE username = 'test.user';

-- Check audit log
SELECT
    table_name,
    operation,
    created_at,
    changed_data::jsonb->>'username' as username
FROM audit_log
ORDER BY created_at DESC
LIMIT 5;
EOF
```

---

## 🔍 Verification and Inspection

### Check Migration History

**PostgreSQL:**
```bash
docker exec -it postgres-default psql -U postgres -d postgres -c \
  "SELECT * FROM flyway_schema_history ORDER BY installed_rank;"
```

**Oracle:**
```bash
docker exec -it oracle-tenant3 sqlplus tenant3_user/tenant3_pass@XEPDB1 << 'EOF'
SELECT * FROM FLYWAY_SCHEMA_HISTORY ORDER BY installed_rank;
EXIT;
EOF
```

### Inspect Schema

**PostgreSQL:**
```bash
# List all tables
docker exec -it postgres-default psql -U postgres -d postgres -c "\dt"

# Describe specific table
docker exec -it postgres-default psql -U postgres -d postgres -c "\d users"

# Check constraints
docker exec -it postgres-default psql -U postgres -d postgres -c \
  "SELECT conname, contype FROM pg_constraint WHERE conrelid = 'users'::regclass;"
```

**Oracle:**
```bash
# List all tables
docker exec -it oracle-tenant3 sqlplus tenant3_user/tenant3_pass@XEPDB1 << 'EOF'
SELECT table_name FROM user_tables ORDER BY table_name;
EXIT;
EOF

# Describe table
docker exec -it oracle-tenant3 sqlplus tenant3_user/tenant3_pass@XEPDB1 << 'EOF'
DESCRIBE users;
EXIT;
EOF
```

### Query Sample Data

```bash
# PostgreSQL: Check seed data
docker exec -it postgres-default psql -U postgres -d postgres << 'EOF'
SELECT username, email, is_active FROM users;
SELECT name, description FROM departments;
SELECT name, status, budget FROM projects;
EOF

# Oracle: Check seed data
docker exec -it oracle-tenant3 sqlplus -S tenant3_user/tenant3_pass@XEPDB1 << 'EOF'
SET PAGESIZE 50
SELECT username, email, is_active FROM users;
EXIT;
EOF
```

---

## 🐛 Troubleshooting

### Issue 1: Flyway Not Found

**Error:** `flyway: command not found`

**Solution:**
```bash
# Use Docker-based commands instead
make migrate-info-docker

# Or install Flyway
brew install flyway  # macOS
```

### Issue 2: Database Connection Refused

**Error:** `Connection refused to localhost:5432`

**Solution:**
```bash
# Ensure databases are running
docker ps | grep postgres

# If not running, start them
make docker-up

# Wait for health checks
docker-compose ps
```

### Issue 3: Migration Checksum Mismatch

**Error:** `Migration checksum mismatch`

**Cause:** You modified an already-applied migration file

**Solution:**
```bash
# Option 1: Never modify applied migrations (RECOMMENDED)
# Create a new versioned migration instead

# Option 2: Repair migration history (USE WITH CAUTION)
flyway -configFiles=flyway/flyway-postgresql.conf repair

# Or via Docker
docker-compose run --rm flyway-postgres repair
```

### Issue 4: Oracle Connection Timeout

**Error:** `Connection timeout to Oracle`

**Cause:** Oracle takes 2-3 minutes to start

**Solution:**
```bash
# Check Oracle startup logs
docker logs -f oracle-tenant3

# Wait for "DATABASE IS READY TO USE" message

# Verify health
docker exec oracle-tenant3 healthcheck.sh
```

### Issue 5: Permission Denied on Migrations

**Error:** `Permission denied: /flyway/sql/V1__*.sql`

**Cause:** File permission issues in Docker volume

**Solution:**
```bash
# Fix permissions
chmod 644 migrations/postgresql/*.sql
chmod 644 migrations/oracle/*.sql

# Restart Flyway container
docker-compose down
docker-compose up -d
```

### Issue 6: Validation Failed in Production

**Error:** `Validation failed: pending migrations detected`

**Cause:** Production environment validates but doesn't execute

**Solution:**
```bash
# Check pending migrations
make migrate-info

# Apply manually if safe
flyway migrate

# Or use migration API endpoint (if enabled)
curl -X POST http://localhost:8080/api/v1/migrations/run
```

---

## 📚 Best Practices

### 1. Migration Naming
- ✅ Use descriptive names: `V1__create_users_table.sql`
- ✅ Increment versions sequentially
- ❌ Don't skip versions (V1, V2, V5 - missing V3, V4)
- ❌ Don't modify applied migrations

### 2. SQL Content
- ✅ Make migrations idempotent when possible (`CREATE IF NOT EXISTS`)
- ✅ Include rollback instructions in comments
- ✅ Test on dev database before production
- ❌ Don't mix DDL and large data changes

### 3. Production Deployment
- ✅ Run migrations before deploying app code
- ✅ Use validation mode initially
- ✅ Monitor migration duration
- ✅ Have rollback plan ready
- ❌ Don't run migrations during peak hours

### 4. Database-Specific Considerations

**PostgreSQL:**
- Use `IF NOT EXISTS` clauses
- Leverage `ON CONFLICT` for upserts
- Use proper index types (btree, gin, etc.)

**Oracle:**
- Use `BEGIN/END` blocks for error handling
- Use `MERGE` for upsert operations
- Be aware of identifier case sensitivity (uppercase default)
- Use `CLOB` for large text fields

### 5. Version Control
- ✅ Commit migrations with related code changes
- ✅ Include in pull requests
- ✅ Document breaking changes
- ❌ Don't modify migrations after merge to main

---

## 🎯 Next Steps

1. **Customize migrations** for your application schema
2. **Add repeatable migrations** for views and stored procedures
3. **Integrate with CI/CD** pipeline
4. **Add migration tests** to verify schema changes
5. **Set up monitoring** for migration failures

## 📖 Additional Resources

- [GoBricks Framework](https://github.com/gaborage/go-bricks)
- [Flyway Documentation](https://flywaydb.org/documentation/)
- [Flyway CLI Reference](https://flywaydb.org/documentation/usage/commandline/)
- [Flyway Best Practices](https://flywaydb.org/documentation/bestpractices)

---

**Happy Migrating! 🚀**
