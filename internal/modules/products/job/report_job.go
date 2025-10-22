package job

import (
	"github.com/gaborage/go-bricks/scheduler"
)

// ReportJob demonstrates publishing messages through JobContext
type ReportJob struct{}

// Execute implements scheduler.Job
func (j *ReportJob) Execute(ctx scheduler.JobContext) error {
	logger := ctx.Logger()
	logger.Info().
		Str("jobID", ctx.JobID()).
		Msg("Generating daily report")

	// Query product's database and generate report...
	// generate txt report file with all products data
	// upload report to storage service (sftp, s3, etc.) -> make it dynamic via interface storage.Upload(destinationPath, fileContents)

	return nil
}
