package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DatabaseBackupSpec defines the desired state of DatabaseBackup
type DatabaseBackupSpec struct {
	// DatabaseType is the type of database to backup (e.g., postgres, mysql)
	// +kubebuilder:validation:Enum=postgres;mysql;mongodb
	DatabaseType string `json:"databaseType"`

	// Schedule in Cron format, see https://en.wikipedia.org/wiki/Cron
	// +kubebuilder:validation:Required
	Schedule string `json:"schedule"`

	// BackupRetention is how long to keep backups (in hours)
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=168
	BackupRetention int64 `json:"backupRetention,omitempty"`

	// StorageDestination defines where to store the backup
	StorageDestination StorageDestinationSpec `json:"storageDestination"`

	// DatabaseSelector selects the target database pods using labels
	// +kubebuilder:validation:Required
	DatabaseSelector metav1.LabelSelector `json:"databaseSelector"`
}

// StorageDestinationSpec defines storage options for backups
type StorageDestinationSpec struct {
	// Type of storage (s3, gcs, pvc)
	// +kubebuilder:validation:Enum=s3;gcs;pvc
	Type string `json:"type"`

	// Bucket name (for S3 or GCS)
	Bucket string `json:"bucket,omitempty"`

	// Path within bucket or PVC
	Path string `json:"path,omitempty"`

	// PVCName is the name of PVC to use (for pvc type)
	PVCName string `json:"pvcName,omitempty"`

	// SecretName containing storage credentials
	SecretName string `json:"secretName,omitempty"`
}

// DatabaseBackupStatus defines the observed state of DatabaseBackup
type DatabaseBackupStatus struct {
	// LastSuccessfulBackup is the timestamp of the last successful backup
	LastSuccessfulBackup *metav1.Time `json:"lastSuccessfulBackup,omitempty"`

	// LastBackupStatus indicates if the last backup succeeded or failed
	LastBackupStatus string `json:"lastBackupStatus,omitempty"`

	// NextScheduledBackup is when the next backup is scheduled
	NextScheduledBackup *metav1.Time `json:"nextScheduledBackup,omitempty"`

	// FailureReason provides more information about failure if the 
	// last backup failed
	FailureReason string `json:"failureReason,omitempty"`

	// ActiveBackupJob is the name of the currently running backup job, if any
	ActiveBackupJob string `json:"activeBackupJob,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Database",type="string",JSONPath=".spec.databaseType"
// +kubebuilder:printcolumn:name="Schedule",type="string",JSONPath=".spec.schedule"
// +kubebuilder:printcolumn:name="Last Backup",type="string",JSONPath=".status.lastSuccessfulBackup"
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.lastBackupStatus"
// DatabaseBackup is the Schema for the databasebackups API
type DatabaseBackup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DatabaseBackupSpec   `json:"spec,omitempty"`
	Status DatabaseBackupStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// DatabaseBackupList contains a list of DatabaseBackup
type DatabaseBackupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DatabaseBackup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DatabaseBackup{}, &DatabaseBackupList{})
}
