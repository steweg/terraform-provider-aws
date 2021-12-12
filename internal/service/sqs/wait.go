package sqs

import (
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go/service/sqs"
	awspolicy "github.com/hashicorp/awspolicyequivalence"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-provider-aws/internal/tfresource"
)

const (
	// Maximum amount of time to wait for SQS queue attribute changes to propagate
	// This timeout should not be increased without strong consideration
	// as this will negatively impact user experience when configurations
	// have incorrect references or permissions.
	// Reference: https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_SetQueueAttributes.html
	queueAttributePropagationTimeout = 1 * time.Minute

	// If you delete a queue, you must wait at least 60 seconds before creating a queue with the same name.
	// ReferenceL https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_CreateQueue.html
	queueCreatedTimeout = 70 * time.Second

	queueDeletedTimeout = 15 * time.Second

	queueStateExists = "exists"
)

func waitQueueAttributesPropagated(conn *sqs.SQS, url string, expected map[string]string) error {
	attributesMatch := func(got map[string]string) error {
		for k, e := range expected {
			g, ok := got[k]

			if !ok {
				// Missing attribute equivalent to empty expected value.
				if e == "" {
					continue
				}

				// Backwards compatibility: https://github.com/hashicorp/terraform-provider-aws/issues/19786.
				if k == sqs.QueueAttributeNameKmsDataKeyReusePeriodSeconds && e == strconv.Itoa(DefaultQueueKMSDataKeyReusePeriodSeconds) {
					continue
				}

				return fmt.Errorf("SQS Queue attribute (%s) not available", k)
			}

			switch k {
			case sqs.QueueAttributeNamePolicy:
				equivalent, err := awspolicy.PoliciesAreEquivalent(g, e)

				if err != nil {
					return err
				}

				if !equivalent {
					return fmt.Errorf("SQS Queue policies are not equivalent")
				}
			case sqs.QueueAttributeNameRedrivePolicy:
				if !StringsEquivalent(g, e) {
					return fmt.Errorf("SQS Queue redrive policies are not equivalent")
				}
			default:
				if g != e {
					return fmt.Errorf("SQS Queue attribute (%s) got: %s, expected: %s", k, g, e)
				}
			}
		}

		return nil
	}

	var got map[string]string
	err := resource.Retry(queueAttributePropagationTimeout, func() *resource.RetryError {
		var err error

		got, err = FindQueueAttributesByURL(conn, url)

		if err != nil {
			return resource.NonRetryableError(err)
		}

		err = attributesMatch(got)

		if err != nil {
			return resource.RetryableError(err)
		}

		return nil
	})

	if tfresource.TimedOut(err) {
		got, err = FindQueueAttributesByURL(conn, url)

		if err != nil {
			return err
		}

		err = attributesMatch(got)
	}

	if err != nil {
		return err
	}

	return nil
}

func waitQueueDeleted(conn *sqs.SQS, url string) error {
	stateConf := &resource.StateChangeConf{
		Pending: []string{queueStateExists},
		Target:  []string{},
		Refresh: statusQueueState(conn, url),
		Timeout: queueDeletedTimeout,

		ContinuousTargetOccurence: 3,
	}

	_, err := stateConf.WaitForState()

	return err
}
