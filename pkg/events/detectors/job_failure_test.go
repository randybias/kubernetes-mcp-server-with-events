package detectors

import (
	"testing"

	"github.com/stretchr/testify/suite"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/containers/kubernetes-mcp-server/pkg/events"
)

// JobFailureDetectorSuite contains tests for JobFailureDetector
type JobFailureDetectorSuite struct {
	suite.Suite
	detector *JobFailureDetector
}

func TestJobFailureDetectorSuite(t *testing.T) {
	suite.Run(t, new(JobFailureDetectorSuite))
}

// SetupTest runs before each test
func (s *JobFailureDetectorSuite) SetupTest() {
	s.detector = NewJobFailureDetector()
}

// TestJobFailureDetector_FailedCondition tests detection of Job failures
func (s *JobFailureDetectorSuite) TestJobFailureDetector_FailedCondition() {
	s.Run("transition to Failed condition emits signal", func() {
		oldJob := createJobWithFailedCondition("test-job", "default", corev1.ConditionFalse, "", "")
		newJob := createJobWithFailedCondition("test-job", "default", corev1.ConditionTrue, "BackoffLimitExceeded", "Job has reached the specified backoff limit")

		signals := s.detector.Detect(oldJob, newJob)

		s.Require().Len(signals, 1)
		signal := signals[0]

		s.Equal(events.FaultTypeJobFailure, signal.FaultType)
		s.Equal(types.UID(newJob.UID), signal.ResourceUID)
		s.Equal("Job", signal.Kind)
		s.Equal("test-job", signal.Name)
		s.Equal("default", signal.Namespace)
		s.Equal(events.SeverityCritical, signal.Severity)
		s.Contains(signal.Context, "Job failed")
		s.Contains(signal.Context, "reason: BackoffLimitExceeded")
		s.Contains(signal.Context, "message: Job has reached the specified backoff limit")
		s.False(signal.Timestamp.IsZero())
	})

	s.Run("transition from no Failed condition to Failed emits signal", func() {
		oldJob := createJobWithoutFailedCondition("test-job", "default")
		newJob := createJobWithFailedCondition("test-job", "default", corev1.ConditionTrue, "DeadlineExceeded", "Job was active longer than specified deadline")

		signals := s.detector.Detect(oldJob, newJob)

		s.Require().Len(signals, 1)
		s.Equal(events.FaultTypeJobFailure, signals[0].FaultType)
		s.Contains(signals[0].Context, "DeadlineExceeded")
	})

	s.Run("no transition when already in Failed state", func() {
		oldJob := createJobWithFailedCondition("test-job", "default", corev1.ConditionTrue, "BackoffLimitExceeded", "Job has failed")
		newJob := createJobWithFailedCondition("test-job", "default", corev1.ConditionTrue, "BackoffLimitExceeded", "Job has failed")

		signals := s.detector.Detect(oldJob, newJob)

		s.Empty(signals, "no state change should not emit signal")
	})

	s.Run("no signal when Failed condition is False", func() {
		oldJob := createJobWithFailedCondition("test-job", "default", corev1.ConditionFalse, "", "")
		newJob := createJobWithFailedCondition("test-job", "default", corev1.ConditionFalse, "", "")

		signals := s.detector.Detect(oldJob, newJob)

		s.Empty(signals, "non-failed job should not emit signal")
	})

	s.Run("recovery from Failed does not emit signal", func() {
		oldJob := createJobWithFailedCondition("test-job", "default", corev1.ConditionTrue, "BackoffLimitExceeded", "Job has failed")
		newJob := createJobWithFailedCondition("test-job", "default", corev1.ConditionFalse, "", "")

		signals := s.detector.Detect(oldJob, newJob)

		s.Empty(signals, "recovery from failure should not emit signal")
	})
}

// TestJobFailureDetector_MultipleConditions tests Jobs with multiple conditions
func (s *JobFailureDetectorSuite) TestJobFailureDetector_MultipleConditions() {
	s.Run("detects Failed among multiple conditions", func() {
		oldJob := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-job",
				Namespace: "default",
				UID:       "job-uid-123",
			},
			Status: batchv1.JobStatus{
				Conditions: []batchv1.JobCondition{
					{
						Type:   batchv1.JobComplete,
						Status: corev1.ConditionFalse,
					},
					{
						Type:   batchv1.JobFailed,
						Status: corev1.ConditionFalse,
					},
				},
			},
		}

		newJob := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-job",
				Namespace: "default",
				UID:       "job-uid-123",
			},
			Status: batchv1.JobStatus{
				Conditions: []batchv1.JobCondition{
					{
						Type:   batchv1.JobComplete,
						Status: corev1.ConditionFalse,
					},
					{
						Type:    batchv1.JobFailed,
						Status:  corev1.ConditionTrue,
						Reason:  "BackoffLimitExceeded",
						Message: "Job has reached the specified backoff limit",
					},
				},
			},
		}

		signals := s.detector.Detect(oldJob, newJob)

		s.Require().Len(signals, 1)
		s.Equal(events.FaultTypeJobFailure, signals[0].FaultType)
		s.Contains(signals[0].Context, "BackoffLimitExceeded")
	})

	s.Run("ignores other condition changes when Failed unchanged", func() {
		oldJob := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-job",
				Namespace: "default",
				UID:       "job-uid-123",
			},
			Status: batchv1.JobStatus{
				Conditions: []batchv1.JobCondition{
					{
						Type:   batchv1.JobComplete,
						Status: corev1.ConditionFalse,
					},
					{
						Type:   batchv1.JobFailed,
						Status: corev1.ConditionFalse,
					},
				},
			},
		}

		newJob := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-job",
				Namespace: "default",
				UID:       "job-uid-123",
			},
			Status: batchv1.JobStatus{
				Conditions: []batchv1.JobCondition{
					{
						Type:   batchv1.JobComplete,
						Status: corev1.ConditionTrue,
					},
					{
						Type:   batchv1.JobFailed,
						Status: corev1.ConditionFalse,
					},
				},
			},
		}

		signals := s.detector.Detect(oldJob, newJob)

		s.Empty(signals, "changes to non-Failed conditions should not emit signal")
	})
}

// TestJobFailureDetector_EdgeCases tests edge cases and error conditions
func (s *JobFailureDetectorSuite) TestJobFailureDetector_EdgeCases() {
	s.Run("returns empty slice for nil newObj", func() {
		oldJob := createJobWithFailedCondition("test-job", "default", corev1.ConditionFalse, "", "")

		signals := s.detector.Detect(oldJob, nil)

		s.Empty(signals)
		s.NotNil(signals)
	})

	s.Run("returns empty slice for nil oldObj", func() {
		newJob := createJobWithFailedCondition("test-job", "default", corev1.ConditionTrue, "BackoffLimitExceeded", "Job has failed")

		signals := s.detector.Detect(nil, newJob)

		s.Empty(signals, "nil oldObj means Add event, no transition to detect")
	})

	s.Run("returns empty slice when newObj is not a Job", func() {
		oldJob := createJobWithFailedCondition("test-job", "default", corev1.ConditionFalse, "", "")
		notAJob := "this is a string, not a Job"

		signals := s.detector.Detect(oldJob, notAJob)

		s.Empty(signals)
	})

	s.Run("returns empty slice when oldObj is not a Job", func() {
		newJob := createJobWithFailedCondition("test-job", "default", corev1.ConditionTrue, "BackoffLimitExceeded", "Job has failed")
		notAJob := "this is a string, not a Job"

		signals := s.detector.Detect(notAJob, newJob)

		s.Empty(signals)
	})

	s.Run("handles Job with no conditions", func() {
		oldJob := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-job",
				Namespace: "default",
				UID:       "job-uid-123",
			},
			Status: batchv1.JobStatus{
				Conditions: []batchv1.JobCondition{},
			},
		}
		newJob := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-job",
				Namespace: "default",
				UID:       "job-uid-123",
			},
			Status: batchv1.JobStatus{
				Conditions: []batchv1.JobCondition{},
			},
		}

		signals := s.detector.Detect(oldJob, newJob)

		s.Empty(signals, "no conditions means no Failed condition to detect")
	})

	s.Run("handles Job with missing Failed condition in newObj", func() {
		oldJob := createJobWithFailedCondition("test-job", "default", corev1.ConditionFalse, "", "")
		newJob := createJobWithoutFailedCondition("test-job", "default")

		signals := s.detector.Detect(oldJob, newJob)

		s.Empty(signals, "missing Failed condition in newObj means no failure to detect")
	})

	s.Run("handles Job with nil conditions", func() {
		oldJob := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-job",
				Namespace: "default",
				UID:       "job-uid-123",
			},
			Status: batchv1.JobStatus{
				Conditions: nil,
			},
		}
		newJob := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-job",
				Namespace: "default",
				UID:       "job-uid-123",
			},
			Status: batchv1.JobStatus{
				Conditions: nil,
			},
		}

		signals := s.detector.Detect(oldJob, newJob)

		s.Empty(signals, "nil conditions should be handled gracefully")
	})
}

// TestJobFailureDetector_ContextBuilding tests context message construction
func (s *JobFailureDetectorSuite) TestJobFailureDetector_ContextBuilding() {
	s.Run("context includes reason and message", func() {
		oldJob := createJobWithFailedCondition("test-job", "default", corev1.ConditionFalse, "", "")
		newJob := createJobWithFailedCondition("test-job", "default", corev1.ConditionTrue, "BackoffLimitExceeded", "Job has reached the specified backoff limit")

		signals := s.detector.Detect(oldJob, newJob)

		s.Require().Len(signals, 1)
		context := signals[0].Context

		s.Contains(context, "Job failed")
		s.Contains(context, "reason: BackoffLimitExceeded")
		s.Contains(context, "message: Job has reached the specified backoff limit")
	})

	s.Run("context includes only reason when message is empty", func() {
		oldJob := createJobWithFailedCondition("test-job", "default", corev1.ConditionFalse, "", "")
		newJob := createJobWithFailedCondition("test-job", "default", corev1.ConditionTrue, "DeadlineExceeded", "")

		signals := s.detector.Detect(oldJob, newJob)

		s.Require().Len(signals, 1)
		context := signals[0].Context

		s.Contains(context, "Job failed")
		s.Contains(context, "reason: DeadlineExceeded")
		s.NotContains(context, "message:")
	})

	s.Run("context is minimal when reason and message are empty", func() {
		oldJob := createJobWithFailedCondition("test-job", "default", corev1.ConditionFalse, "", "")
		newJob := createJobWithFailedCondition("test-job", "default", corev1.ConditionTrue, "", "")

		signals := s.detector.Detect(oldJob, newJob)

		s.Require().Len(signals, 1)
		context := signals[0].Context

		s.Equal("Job failed", context)
		s.NotContains(context, "reason:")
		s.NotContains(context, "message:")
	})

	s.Run("context includes message when reason is empty", func() {
		oldJob := createJobWithFailedCondition("test-job", "default", corev1.ConditionFalse, "", "")
		newJob := createJobWithFailedCondition("test-job", "default", corev1.ConditionTrue, "", "Job execution failed")

		signals := s.detector.Detect(oldJob, newJob)

		s.Require().Len(signals, 1)
		context := signals[0].Context

		s.Contains(context, "Job failed")
		s.Contains(context, "message: Job execution failed")
		s.NotContains(context, "reason:")
	})
}

// TestJobFailureDetector_SeverityLevels tests severity determination
func (s *JobFailureDetectorSuite) TestJobFailureDetector_SeverityLevels() {
	s.Run("BackoffLimitExceeded results in critical severity", func() {
		oldJob := createJobWithFailedCondition("test-job", "default", corev1.ConditionFalse, "", "")
		newJob := createJobWithFailedCondition("test-job", "default", corev1.ConditionTrue, "BackoffLimitExceeded", "Job has reached the specified backoff limit")

		signals := s.detector.Detect(oldJob, newJob)

		s.Require().Len(signals, 1)
		s.Equal(events.SeverityCritical, signals[0].Severity)
	})

	s.Run("DeadlineExceeded results in warning severity", func() {
		oldJob := createJobWithFailedCondition("test-job", "default", corev1.ConditionFalse, "", "")
		newJob := createJobWithFailedCondition("test-job", "default", corev1.ConditionTrue, "DeadlineExceeded", "Job was active longer than specified deadline")

		signals := s.detector.Detect(oldJob, newJob)

		s.Require().Len(signals, 1)
		s.Equal(events.SeverityWarning, signals[0].Severity)
	})

	s.Run("other failure reasons result in warning severity", func() {
		oldJob := createJobWithFailedCondition("test-job", "default", corev1.ConditionFalse, "", "")
		newJob := createJobWithFailedCondition("test-job", "default", corev1.ConditionTrue, "SomeOtherReason", "Job failed for some reason")

		signals := s.detector.Detect(oldJob, newJob)

		s.Require().Len(signals, 1)
		s.Equal(events.SeverityWarning, signals[0].Severity)
	})

	s.Run("empty reason results in warning severity", func() {
		oldJob := createJobWithFailedCondition("test-job", "default", corev1.ConditionFalse, "", "")
		newJob := createJobWithFailedCondition("test-job", "default", corev1.ConditionTrue, "", "Job failed")

		signals := s.detector.Detect(oldJob, newJob)

		s.Require().Len(signals, 1)
		s.Equal(events.SeverityWarning, signals[0].Severity)
	})
}

// TestJobFailureDetector_DetectorInterface verifies interface compliance
func (s *JobFailureDetectorSuite) TestJobFailureDetector_DetectorInterface() {
	s.Run("JobFailureDetector implements Detector interface", func() {
		var _ events.Detector = &JobFailureDetector{}
		var _ events.Detector = s.detector
	})
}

// TestGetFailedCondition tests the getFailedCondition helper function
func (s *JobFailureDetectorSuite) TestGetFailedCondition() {
	s.Run("finds Failed condition", func() {
		job := createJobWithFailedCondition("test-job", "default", corev1.ConditionTrue, "BackoffLimitExceeded", "Job has failed")

		condition := getFailedCondition(job)

		s.Require().NotNil(condition)
		s.Equal(batchv1.JobFailed, condition.Type)
		s.Equal(corev1.ConditionTrue, condition.Status)
		s.Equal("BackoffLimitExceeded", condition.Reason)
		s.Equal("Job has failed", condition.Message)
	})

	s.Run("returns nil when Failed condition not found", func() {
		job := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-job",
				Namespace: "default",
				UID:       "job-uid-123",
			},
			Status: batchv1.JobStatus{
				Conditions: []batchv1.JobCondition{
					{
						Type:   batchv1.JobComplete,
						Status: corev1.ConditionTrue,
					},
				},
			},
		}

		condition := getFailedCondition(job)

		s.Nil(condition)
	})

	s.Run("returns nil when Job is nil", func() {
		condition := getFailedCondition(nil)

		s.Nil(condition)
	})

	s.Run("returns nil when Job has no conditions", func() {
		job := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-job",
				Namespace: "default",
				UID:       "job-uid-123",
			},
			Status: batchv1.JobStatus{
				Conditions: []batchv1.JobCondition{},
			},
		}

		condition := getFailedCondition(job)

		s.Nil(condition)
	})

	s.Run("returns nil when Job has nil conditions", func() {
		job := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-job",
				Namespace: "default",
				UID:       "job-uid-123",
			},
			Status: batchv1.JobStatus{
				Conditions: nil,
			},
		}

		condition := getFailedCondition(job)

		s.Nil(condition)
	})

	s.Run("finds Failed condition among multiple conditions", func() {
		job := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-job",
				Namespace: "default",
				UID:       "job-uid-123",
			},
			Status: batchv1.JobStatus{
				Conditions: []batchv1.JobCondition{
					{
						Type:   batchv1.JobComplete,
						Status: corev1.ConditionFalse,
					},
					{
						Type:   batchv1.JobSuspended,
						Status: corev1.ConditionFalse,
					},
					{
						Type:    batchv1.JobFailed,
						Status:  corev1.ConditionTrue,
						Reason:  "BackoffLimitExceeded",
						Message: "Job has failed",
					},
				},
			},
		}

		condition := getFailedCondition(job)

		s.Require().NotNil(condition)
		s.Equal(batchv1.JobFailed, condition.Type)
		s.Equal(corev1.ConditionTrue, condition.Status)
	})
}

// Helper function to create a Job with a Failed condition
func createJobWithFailedCondition(name, namespace string, status corev1.ConditionStatus, reason, message string) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       "job-uid-123",
		},
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{
				{
					Type:    batchv1.JobFailed,
					Status:  status,
					Reason:  reason,
					Message: message,
				},
			},
		},
	}
}

// Helper function to create a Job without a Failed condition
func createJobWithoutFailedCondition(name, namespace string) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       "job-uid-123",
		},
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{
				{
					Type:   batchv1.JobComplete,
					Status: corev1.ConditionFalse,
				},
			},
		},
	}
}
