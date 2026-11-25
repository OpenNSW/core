// Package tasktypes holds the task types any single window can use: the ones a
// template names in a step's type and that need nothing specific to one
// deployment. Each has a TaskType constant and a constructor, and the
// application registers the ones it uses on its plugin registry.
package tasktypes

// Task type keys. These must match the SubTaskTemplate.Type values declared
// in the JSON configs loaded into the artifact registry.
const (
	TaskTypeUserInput      = "USER_INPUT"
	TaskTypeExternalReview = "EXTERNAL_REVIEW"
	TaskTypePayment        = "PAYMENT"
	TaskTypeAPICall        = "API_CALL"
	TaskTypeAuthAPICall    = "AUTH_API_CALL"
	TaskTypeNotification   = "NOTIFICATION"
)
