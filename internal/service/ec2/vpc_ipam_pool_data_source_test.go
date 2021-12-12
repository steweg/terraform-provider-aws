package ec2_test

import (
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
	"github.com/hashicorp/terraform-provider-aws/internal/acctest"
)

func TestAccDataSourceVPCIpamPool_basic(t *testing.T) {
	resourceName := "aws_vpc_ipam_pool.test"
	dataSourceName := "data.aws_vpc_ipam_pool.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:   func() { acctest.PreCheck(t) },
		ErrorCheck: acctest.ErrorCheck(t, ec2.EndpointsID),
		Providers:  acctest.Providers,
		Steps: []resource.TestStep{
			{
				Config: testAccVPCIpamPoolOptions,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckDataSourceVPCIpamPoolID(dataSourceName),
					resource.TestCheckResourceAttrPair(dataSourceName, "arn", resourceName, "arn"),
					resource.TestCheckResourceAttrPair(dataSourceName, "auto_import", resourceName, "auto_import"),
					resource.TestCheckResourceAttrPair(dataSourceName, "ipam_scope_id", resourceName, "ipam_scope_id"),
					resource.TestCheckResourceAttrPair(dataSourceName, "ipam_scope_type", resourceName, "ipam_scope_type"),
					resource.TestCheckResourceAttrPair(dataSourceName, "locale", resourceName, "locale"),
					resource.TestCheckResourceAttrPair(dataSourceName, "pool_depth", resourceName, "pool_depth"),
					resource.TestCheckResourceAttrPair(dataSourceName, "state", resourceName, "state"),
					resource.TestCheckResourceAttrPair(dataSourceName, "tags", resourceName, "tags"),
				),
			},
		},
	})
}

func testAccCheckDataSourceVPCIpamPoolID(n string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("Can't find ipam pool: %s", n)
		}

		if rs.Primary.ID == "" {
			return fmt.Errorf("ipam pool ID not set")
		}
		return nil
	}
}

const testAccVPCIpamPoolOptions = testAccVPCIpamPoolBase + `
resource "aws_vpc_ipam_pool" "test" {
  address_family                    = "ipv4"
  ipam_scope_id                     = aws_vpc_ipam.test.private_default_scope_id
  auto_import                       = true
  allocation_default_netmask_length = 32
  allocation_max_netmask_length     = 32
  allocation_min_netmask_length     = 32
  allocation_resource_tags = {
    test = "1"
  }
  description = "test"
}

data "aws_vpc_ipam_pool" "test" {
  depends_on = [aws_vpc_ipam_pool.test]
}
`
