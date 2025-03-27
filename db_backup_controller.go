package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/robfig/cron"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	dbbackupv1alpha1 "github.com/example/db-backup-operator/api/v1alpha1"
)

// DatabaseBackupReconciler reconciles a DatabaseBackup object
type DatabaseBackupReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=db.example.io,resources=databasebackups,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=db.example.io,resources=databasebackups/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=db.example.io,resources=databasebackups/finalizers,verbs=update
//+kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch

func (r *DatabaseBackupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithValues("databasebackup", req.NamespacedName)

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

	// Initialize status if it doesn't exist
	if dbBackup.Status.LastBackupStatus == "" {
		dbBackup.Status.LastBackupStatus = "Pending"
		if err := r.Status().Update(ctx, &dbBackup); err != nil {
			log.Error(err, "Failed to update status")
			return ctrl.Result{}, err
		}
	}

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

			// Clear active job field
			dbBackup.Status.ActiveBackupJob = ""
			if err := r.Status().Update(ctx, &dbBackup); err != nil {
				log.Error(err, "Failed to update status after job completion")
				return ctrl.Result{}, err
			}
		}
	}

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

		// Update status with active job
		dbBackup.Status.ActiveBackupJob = job.Name
		dbBackup.Status.LastBackupStatus = "Running"
		if err := r.Status().Update(ctx, &dbBackup); err != nil {
			log.Error(err, "Failed to update status with active job")
			return ctrl.Result{}, err
		}

		// Calculate next run
		nextRun = schedule.Next(time.Now())
		dbBackup.Status.NextScheduledBackup = &metav1.Time{Time: nextRun}
		if err := r.Status().Update(ctx, &dbBackup); err != nil {
			log.Error(err, "Failed to update next scheduled backup time")
			return ctrl.Result{}, err
		}
	}

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

// Helper function to check if a job is complete
func isJobComplete(job *batchv1.Job) bool {
	for _, c := range job.Status.Conditions {
		if (c.Type == batchv1.JobComplete || c.Type == batchv1.JobFailed) && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// Helper function to check if a job is successful
func isJobSuccessful(job *batchv1.Job) bool {
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// Helper function to check if a job has failed
func isJobFailed(job *batchv1.Job) bool {
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// Helper function to check if it's time to run a backup
func isTimeToBackup(nextScheduled *metav1.Time) bool {
	if nextScheduled == nil {
		return true // If next scheduled time is not set, run a backup
	}
	return time.Now().After(nextScheduled.Time) || time.Now().Equal(nextScheduled.Time)
}

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

	if err := ctrl.SetControllerReference(dbBackup, job, r.Scheme); err != nil {
		return nil, err
	}

	if err := r.Create(ctx, job); err != nil {
		return nil, err
	}

	return job, nil
}

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

// SetupWithManager sets up the controller with the Manager.
func (r *DatabaseBackupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&dbbackupv1alpha1.DatabaseBackup{}).
		Owns(&batchv1.Job{}).
		Complete(r)
}
