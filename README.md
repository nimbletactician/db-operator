# Database Backup Controller Walkthrough

This is a line-by-line explanation of the database backup controller implementation.

## DatabaseBackupReconciler Structure

```go
type DatabaseBackupReconciler struct {
    client.Client
    Scheme *runtime.Scheme
}
```

The `DatabaseBackupReconciler` struct is the core of our controller. It embeds a Kubernetes client and holds a reference to a runtime scheme which is used for converting between Go types and Kubernetes API objects.

## Reconcile Function

The `Reconcile` function is the heart of the controller, implementing the control loop that brings the system from current state to desired state.

```go
func (r *DatabaseBackupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    log := log.FromContext(ctx).WithValues("databasebackup", req.NamespacedName)
```

- Takes a context and a request containing the name/namespace of the object being reconciled
- Sets up structured logging with relevant metadata

```go
    // Fetch the DatabaseBackup instance
    var dbBackup dbbackupv1alpha1.DatabaseBackup
    if err := r.Get(ctx, req.NamespacedName, &dbBackup); err != nil {
        if errors.IsNotFound(err) {
            // Object not found, could have been deleted
            return ctrl.Result{}, nil
        }
        // Error reading the object
        log.Error(err, "Failed to get DatabaseBackup")
        return ctrl.Result{}, err
    }
```

- Retrieves the DatabaseBackup custom resource that triggered reconciliation
- If the object is not found (likely deleted), returns without error
- If there's any other error retrieving the object, logs the error and returns with error

```go
    // Initialize status if it doesn't exist
    if dbBackup.Status.LastBackupStatus == "" {
        dbBackup.Status.LastBackupStatus = "Pending"
        if err := r.Status().Update(ctx, &dbBackup); err != nil {
            log.Error(err, "Failed to update status")
            return ctrl.Result{}, err
        }
    }
```

- Checks if the status field is initialized
- If not, sets the status to "Pending" and updates the status subresource
- Returns any errors from the status update

## Check Active Job Status

```go
    // Check if there's an active backup job
    if dbBackup.Status.ActiveBackupJob != "" {
        var job batchv1.Job
        jobName := types.NamespacedName{
            Name:      dbBackup.Status.ActiveBackupJob,
            Namespace: req.Namespace,
        }

        err := r.Get(ctx, jobName, &job)
        if err != nil && !errors.IsNotFound(err) {
            log.Error(err, "Failed to get active backup job")
            return ctrl.Result{}, err
        }
```

- If there's an active backup job name in the status, tries to retrieve the job
- If the retrieval fails with an error other than "not found", returns with error

```go
        // If job is completed or not found, clear the active job field
        if errors.IsNotFound(err) || isJobComplete(&job) {
            // If job completed successfully, update last successful backup time
            if err == nil && isJobSuccessful(&job) {
                now := metav1.Now()
                dbBackup.Status.LastSuccessfulBackup = &now
                dbBackup.Status.LastBackupStatus = "Succeeded"
                dbBackup.Status.FailureReason = ""
            } else if err == nil && isJobFailed(&job) {
                dbBackup.Status.LastBackupStatus = "Failed"
                dbBackup.Status.FailureReason = "Backup job failed, check job logs for details"
            }
```

- If the job is not found or is complete, processes its status
- For successful jobs, updates the last successful backup timestamp and clears any error reason
- For failed jobs, sets the status to "Failed" and provides a generic failure reason

```go
            // Clear active job field
            dbBackup.Status.ActiveBackupJob = ""
            if err := r.Status().Update(ctx, &dbBackup); err != nil {
                log.Error(err, "Failed to update status after job completion")
                return ctrl.Result{}, err
            }
        }
    }
```

- Clears the active job field to indicate no active job is running
- Updates the status and returns any errors from the update

## Schedule Processing

```go
    // Calculate next run based on cron schedule
    schedule, err := cron.ParseStandard(dbBackup.Spec.Schedule)
    if err != nil {
        log.Error(err, "Failed to parse schedule", "schedule", dbBackup.Spec.Schedule)
        dbBackup.Status.LastBackupStatus = "Error"
        dbBackup.Status.FailureReason = fmt.Sprintf("Invalid schedule: %v", err)
        if err := r.Status().Update(ctx, &dbBackup); err != nil {
            return ctrl.Result{}, err
        }
        return ctrl.Result{}, nil
    }
```

- Parses the cron schedule from the spec
- If the schedule is invalid, updates the status with an error and returns

```go
    // Calculate next scheduled run
    nextRun := schedule.Next(time.Now())
    nextRunMetaTime := metav1.NewTime(nextRun)
    
    // Update next scheduled backup if it's changed
    if dbBackup.Status.NextScheduledBackup == nil || 
        !dbBackup.Status.NextScheduledBackup.Equal(&nextRunMetaTime) {
        dbBackup.Status.NextScheduledBackup = &nextRunMetaTime
        if err := r.Status().Update(ctx, &dbBackup); err != nil {
            log.Error(err, "Failed to update next scheduled backup time")
            return ctrl.Result{}, err
        }
    }
```

- Calculates the next scheduled run time based on the current time
- If the next scheduled backup time is not set or has changed, updates it
- Returns any errors from the status update

## Creating a New Backup Job

```go
    // If no active backup job and it's time to run one
    if dbBackup.Status.ActiveBackupJob == "" && isTimeToBackup(dbBackup.Status.NextScheduledBackup) {
        // Create a backup job
        job, err := r.createBackupJob(ctx, &dbBackup)
        if err != nil {
            log.Error(err, "Failed to create backup job")
            dbBackup.Status.LastBackupStatus = "Error"
            dbBackup.Status.FailureReason = fmt.Sprintf("Failed to create backup job: %v", err)
            if updateErr := r.Status().Update(ctx, &dbBackup); updateErr != nil {
                log.Error(updateErr, "Failed to update status after job creation failure")
            }
            return ctrl.Result{}, err
        }
```

- If there's no active job and it's time to run a backup, creates a new backup job
- If job creation fails, updates the status with an error and returns

```go
        // Update status with active job
        dbBackup.Status.ActiveBackupJob = job.Name
        dbBackup.Status.LastBackupStatus = "Running"
        if err := r.Status().Update(ctx, &dbBackup); err != nil {
            log.Error(err, "Failed to update status with active job")
            return ctrl.Result{}, err
        }
```

- Updates the status to indicate a running job
- Sets the active job name to the newly created job
- Returns any errors from the status update

```go
        // Calculate next run
        nextRun = schedule.Next(time.Now())
        dbBackup.Status.NextScheduledBackup = &metav1.Time{Time: nextRun}
        if err := r.Status().Update(ctx, &dbBackup); err != nil {
            log.Error(err, "Failed to update next scheduled backup time")
            return ctrl.Result{}, err
        }
    }
```

- Recalculates the next run time after starting the current job
- Updates the next scheduled backup time
- Returns any errors from the status update

## Requeue Logic

```go
    // Requeue based on next scheduled backup
    var requeueAfter time.Duration
    if dbBackup.Status.NextScheduledBackup != nil {
        requeueAfter = time.Until(dbBackup.Status.NextScheduledBackup.Time)
        if requeueAfter < 0 {
            requeueAfter = time.Second // Requeue immediately if we're past the scheduled time
        }
    } else {
        requeueAfter = time.Minute // Default requeue time if next backup time is not set
    }

    return ctrl.Result{RequeueAfter: requeueAfter}, nil
}
```

- Calculates how long to wait before the next reconciliation
- If the next scheduled time is set, waits until that time
- If the scheduled time is in the past, requeues immediately
- If no schedule is set, uses a default one-minute interval
- Returns a result that will trigger requeuing after the calculated duration

## Helper Functions

### isJobComplete

```go
// Helper function to check if a job is complete
func isJobComplete(job *batchv1.Job) bool {
    for _, c := range job.Status.Conditions {
        if (c.Type == batchv1.JobComplete || c.Type == batchv1.JobFailed) && c.Status == corev1.ConditionTrue {
            return true
        }
    }
    return false
}
```

- Checks if a job is complete by examining its status conditions
- Returns true if the job has either completed successfully or failed

### isJobSuccessful

```go
// Helper function to check if a job is successful
func isJobSuccessful(job *batchv1.Job) bool {
    for _, c := range job.Status.Conditions {
        if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
            return true
        }
    }
    return false
}
```

- Checks if a job has completed successfully
- Returns true only if the job has a "Complete" condition with status "True"

### isJobFailed

```go
// Helper function to check if a job has failed
func isJobFailed(job *batchv1.Job) bool {
    for _, c := range job.Status.Conditions {
        if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
            return true
        }
    }
    return false
}
```

- Checks if a job has failed
- Returns true only if the job has a "Failed" condition with status "True"

### isTimeToBackup

```go
// Helper function to check if it's time to run a backup
func isTimeToBackup(nextScheduled *metav1.Time) bool {
    if nextScheduled == nil {
        return true // If next scheduled time is not set, run a backup
    }
    return time.Now().After(nextScheduled.Time) || time.Now().Equal(nextScheduled.Time)
}
```

- Determines if it's time to run a backup based on the next scheduled time
- If no schedule is set, returns true to trigger an immediate backup
- Otherwise, returns true if the current time is at or after the scheduled time

## Job Creation Logic

```go
// Helper function to create a backup job
func (r *DatabaseBackupReconciler) createBackupJob(ctx context.Context, dbBackup *dbbackupv1alpha1.DatabaseBackup) (*batchv1.Job, error) {
    backupImage := getBackupImage(dbBackup.Spec.DatabaseType)
    
    job := &batchv1.Job{
        ObjectMeta: metav1.ObjectMeta{
            Name:      fmt.Sprintf("%s-%s", dbBackup.Name, time.Now().Format("20060102150405")),
            Namespace: dbBackup.Namespace,
            Labels: map[string]string{
                "app":                    "db-backup-operator",
                "databasebackup.db.example.io/name": dbBackup.Name,
            },
        },
        Spec: batchv1.JobSpec{
            Template: corev1.PodTemplateSpec{
                Spec: corev1.PodSpec{
                    RestartPolicy: corev1.RestartPolicyNever,
                    Containers: []corev1.Container{
                        {
                            Name:  "backup",
                            Image: backupImage,
                            Env: []corev1.EnvVar{
                                {
                                    Name:  "DB_TYPE",
                                    Value: dbBackup.Spec.DatabaseType,
                                },
                                {
                                    Name:  "STORAGE_TYPE",
                                    Value: dbBackup.Spec.StorageDestination.Type,
                                },
                                {
                                    Name:  "BUCKET",
                                    Value: dbBackup.Spec.StorageDestination.Bucket,
                                },
                                {
                                    Name:  "PATH",
                                    Value: dbBackup.Spec.StorageDestination.Path,
                                },
                            },
                        },
                    },
                },
            },
        },
    }
```

- Creates a new Kubernetes Job for running the backup
- Uses a timestamp in the job name to make it unique
- Sets appropriate labels to track which DatabaseBackup created the job
- Configures the job with:
  - A container image specific to the database type
  - Environment variables for database and storage configuration
  - A "Never" restart policy to avoid infinite retry loops

```go
    // If using PVC for storage, add volume and volume mount
    if dbBackup.Spec.StorageDestination.Type == "pvc" && dbBackup.Spec.StorageDestination.PVCName != "" {
        job.Spec.Template.Spec.Volumes = []corev1.Volume{
            {
                Name: "backup-storage",
                VolumeSource: corev1.VolumeSource{
                    PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
                        ClaimName: dbBackup.Spec.StorageDestination.PVCName,
                    },
                },
            },
        }
        job.Spec.Template.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{
            {
                Name:      "backup-storage",
                MountPath: "/backups",
            },
        }
    }
```

- If using PVC storage, adds a volume and volume mount to the job
- Mounts the specified PVC at "/backups" in the container

```go
    // If storage credentials are provided, add secret volume
    if dbBackup.Spec.StorageDestination.SecretName != "" {
        job.Spec.Template.Spec.Volumes = append(job.Spec.Template.Spec.Volumes, corev1.Volume{
            Name: "storage-credentials",
            VolumeSource: corev1.VolumeSource{
                Secret: &corev1.SecretVolumeSource{
                    SecretName: dbBackup.Spec.StorageDestination.SecretName,
                },
            },
        })
        job.Spec.Template.Spec.Containers[0].VolumeMounts = append(
            job.Spec.Template.Spec.Containers[0].VolumeMounts,
            corev1.VolumeMount{
                Name:      "storage-credentials",
                MountPath: "/credentials",
            },
        )
    }
```

- If a storage credentials secret is specified, adds it as a volume
- Mounts the secret at "/credentials" in the container

```go
    if err := ctrl.SetControllerReference(dbBackup, job, r.Scheme); err != nil {
        return nil, err
    }

    if err := r.Create(ctx, job); err != nil {
        return nil, err
    }

    return job, nil
}
```

- Sets up ownership references so the job is garbage-collected when the DatabaseBackup is deleted
- Creates the job in the Kubernetes cluster
- Returns the created job or any error that occurred

### getBackupImage

```go
// Helper function to get the appropriate backup image based on DB type
func getBackupImage(dbType string) string {
    switch dbType {
    case "postgres":
        return "ghcr.io/example/postgres-backup:latest"
    case "mysql":
        return "ghcr.io/example/mysql-backup:latest"
    case "mongodb":
        return "ghcr.io/example/mongodb-backup:latest"
    default:
        return "ghcr.io/example/generic-backup:latest"
    }
}
```

- Returns the appropriate container image for the specified database type
- Uses a default generic image for unrecognized database types

## Controller Setup

```go
// SetupWithManager sets up the controller with the Manager.
func (r *DatabaseBackupReconciler) SetupWithManager(mgr ctrl.Manager) error {
    return ctrl.NewControllerManagedBy(mgr).
        For(&dbbackupv1alpha1.DatabaseBackup{}).
        Owns(&batchv1.Job{}).
        Complete(r)
}
```

- Configures the controller with the manager
- Watches for changes to DatabaseBackup resources
- Also watches for changes to Jobs owned by DatabaseBackup resources
- Completes the controller setup with the reconciler
