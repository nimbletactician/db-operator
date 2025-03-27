# DatabaseBackup Controller

A Kubernetes operator for managing scheduled database backups.

## Overview

The DatabaseBackup controller automates the process of backing up databases running in Kubernetes. It follows a declarative approach where users create DatabaseBackup custom resources to define their backup requirements, and the controller handles scheduling, execution, and monitoring of backup jobs.

## Features

- Scheduled backups using cron expressions
- Support for PostgreSQL databases (extensible to other database types)
- Cloud storage integration (S3)
- Configurable backup retention periods
- Database selection via Kubernetes labels
- Automatic backup status tracking and monitoring

## Custom Resource Definition

The `DatabaseBackup` custom resource allows you to define your backup configuration:

```yaml
apiVersion: db.example.io/v1alpha1
kind: DatabaseBackup
metadata:
  name: postgres-daily-backup
  namespace: default
spec:
  databaseType: postgres
  schedule: "0 1 * * *"  # Daily at 1 AM
  backupRetention: 336   # 14 days (in hours)
  storageDestination:
    type: s3
    bucket: my-database-backups
    path: postgres/daily
    secretName: s3-credentials
  databaseSelector:
    matchLabels:
      app: postgres
      role: primary
```

## Controller Reconciliation Process

The controller follows a carefully designed reconciliation loop to ensure your backups run as scheduled:

1. **Request Processing**: Controller receives reconcile request for a DatabaseBackup CR
2. **Resource Retrieval**: Fetches the DatabaseBackup resource and its current state
3. **Status Initialization**: Initializes status fields if first reconciliation
4. **Active Job Check**: Verifies if a backup job is already running
5. **Schedule Validation**: Parses and validates the cron expression
6. **Next Run Calculation**: Determines when the next backup should run
7. **Backup Decision**: Determines if a backup job needs to be created now
8. **Job Creation**: Creates a Kubernetes Job to perform the backup when needed
9. **Status Updates**: Maintains accurate status information throughout the process
10. **Requeue Timing**: Schedules next controller reconciliation based on backup schedule
11. **Job Monitoring**: Tracks backup job progress and updates status accordingly
12. **Failure Handling**: Records and reports any backup failures
13. **Cycle Completion**: Prepares for next scheduled backup

## Status Fields

The controller maintains several status fields to track backup operations:

- `LastBackupStatus`: Current status of the most recent backup (Pending, Running, Succeeded, Failed)
- `NextScheduledBackup`: Timestamp of the next scheduled backup
- `ActiveBackupJob`: Name of the currently running backup job (if any)
- `LastSuccessfulBackup`: Timestamp of the most recent successful backup
- `FailureReason`: Details about backup failures (if any)

## Usage Example

1. Create a Secret containing your S3 credentials:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: s3-credentials
  namespace: default
type: Opaque
data:
  AWS_ACCESS_KEY_ID: <base64-encoded-access-key>
  AWS_SECRET_ACCESS_KEY: <base64-encoded-secret-key>
```

2. Deploy your database with appropriate labels:

```yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: postgres
  namespace: default
spec:
  selector:
    matchLabels:
      app: postgres
  template:
    metadata:
      labels:
        app: postgres
        role: primary
    # ... rest of your database configuration
```

3. Create your DatabaseBackup resource:

```yaml
apiVersion: db.example.io/v1alpha1
kind: DatabaseBackup
metadata:
  name: postgres-daily-backup
  namespace: default
spec:
  databaseType: postgres
  schedule: "0 1 * * *"  # Daily at 1 AM
  backupRetention: 336   # 14 days (in hours)
  storageDestination:
    type: s3
    bucket: my-database-backups
    path: postgres/daily
    secretName: s3-credentials
  databaseSelector:
    matchLabels:
      app: postgres
      role: primary
```

4. Monitor backup status:

```bash
kubectl get databasebackups
kubectl describe databasebackup postgres-daily-backup
```

## Troubleshooting

If backups are failing, check:

1. The FailureReason in the DatabaseBackup status
2. Logs from the backup job itself
3. Verify S3 credentials and permissions
4. Ensure the database is accessible using the selector labels

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.
