Controller Initialization

  The initialization starts in main.go with these key steps:

  1. Scheme Registration: The application registers both standard Kubernetes types and your custom DatabaseBackup type to the runtime scheme, enabling serialization/deserialization
  of these resources.
  2. Manager Setup: A controller-runtime Manager is created, which:
    - Sets up caching for watched resources
    - Provides a client for CRUD operations
    - Configures metrics, health probes, and leader election
  3. Controller Registration: The DatabaseBackupReconciler is instantiated and registered with the manager, configured to watch DatabaseBackup resources and the Jobs it creates.

  Reconciliation Loop

  The core of the controller is the Reconcile method, which:

  1. Fetches the CR: Gets the DatabaseBackup resource that triggered reconciliation
  2. Manages Status: Checks and updates status fields:
    - Initializes status when first created
    - Tracks active backup jobs
    - Updates timestamps for successful backups
    - Records failure reasons
  3. Schedule Processing:
    - Parses the cron schedule
    - Calculates next run time
    - Determines if a backup should be triggered now
  4. Job Management:
    - Creates Jobs for executing the actual backup
    - Configures the job with appropriate container image and environment variables
    - Sets up storage volumes based on configuration (S3, GCS, PVC)
  5. Requeue Logic:
    - Schedules the next reconciliation to match the backup schedule

  Complete Flow with Sample CR

  When you deploy the sample PostgreSQL backup CR:

  1. CR Creation:
    - The postgres-daily-backup CR is submitted to the Kubernetes API
    - The API server validates it against CRD schema
    - The controller receives a watch event
  2. First Reconciliation:
    - Status is initialized to "Pending"
    - Schedule "0 1 * * *" is parsed
    - Next scheduled backup time is calculated (1 AM next occurrence)
    - Controller requeues until scheduled time
  3. Backup Execution:
    - When the scheduled time arrives:
        - Controller creates a backup Job named like postgres-daily-backup-20250327010000
      - Job uses ghcr.io/example/postgres-backup:latest image
      - S3 storage configuration is set via environment variables
      - S3 credentials are mounted from the s3-credentials secret
      - Controller sets CR status to "Running" with the active job name
  4. Job Completion:
    - When job completes:
        - Controller updates status to "Succeeded" or "Failed"
      - Updates lastSuccessfulBackup timestamp if successful
      - Records failureReason if failed
      - Sets next backup for 1 AM the following day
  5. Continuous Operation:
    - Process repeats daily at 1 AM
    - Backups are stored at s3://my-database-backups/postgres/daily
    - Retained for 14 days (336 hours)

  The controller demonstrates the Kubernetes operator pattern effectively by:
  - Converting declarative specs into imperative actions
  - Continuously reconciling to maintain the desired state
  - Leveraging Kubernetes Jobs for the actual work
  - Providing status updates and next steps information
