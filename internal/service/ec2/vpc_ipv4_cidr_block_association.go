package ec2

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/hashicorp/aws-sdk-go-base/tfawserr"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
	"github.com/hashicorp/terraform-provider-aws/internal/conns"
)

const (
	VpcCidrBlockStateCodeDeleted = "deleted"
)

func ResourceVPCIPv4CIDRBlockAssociation() *schema.Resource {
	return &schema.Resource{
		Create: resourceVPCIPv4CIDRBlockAssociationCreate,
		Read:   resourceVPCIPv4CIDRBlockAssociationRead,
		Delete: resourceVPCIPv4CIDRBlockAssociationDelete,
		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},
		CustomizeDiff: func(_ context.Context, diff *schema.ResourceDiff, v interface{}) error {
			// cidr_block can be set by a value returned from IPAM or explicitly in config
			if diff.Id() != "" && diff.HasChange("cidr_block") {
				// if netmask is set then cidr_block is derived from ipam, ignore changes
				if diff.Get("ipv4_netmask_length") != 0 {
					return diff.Clear("cidr_block")
				}
				return diff.ForceNew("cidr_block")
			}
			return nil
		},
		Schema: map[string]*schema.Schema{
			"vpc_id": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},
			"cidr_block": {
				Type:         schema.TypeString,
				Optional:     true,
				Computed:     true,
				ForceNew:     true,
				ValidateFunc: validation.IsCIDRNetwork(VPCCIDRMinIPv4, VPCCIDRMaxIPv4),
			},
			"ipv4_ipam_pool_id": {
				Type:     schema.TypeString,
				Optional: true,
				ForceNew: true,
			},
			"ipv4_netmask_length": {
				Type:         schema.TypeInt,
				Optional:     true,
				ForceNew:     true,
				ValidateFunc: validation.IntBetween(VPCCIDRMinIPv4, VPCCIDRMaxIPv4),
			},
		},

		Timeouts: &schema.ResourceTimeout{
			Create: schema.DefaultTimeout(10 * time.Minute),
			Delete: schema.DefaultTimeout(10 * time.Minute),
		},
	}
}

func resourceVPCIPv4CIDRBlockAssociationCreate(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*conns.AWSClient).EC2Conn

	req := &ec2.AssociateVpcCidrBlockInput{
		VpcId: aws.String(d.Get("vpc_id").(string)),
	}

	if v, ok := d.GetOk("cidr_block"); ok {
		req.CidrBlock = aws.String(v.(string))
	}

	if v, ok := d.GetOk("ipv4_ipam_pool_id"); ok {
		req.Ipv4IpamPoolId = aws.String(v.(string))
	}

	if v, ok := d.GetOk("ipv4_netmask_length"); ok {
		req.Ipv4NetmaskLength = aws.Int64(int64(v.(int)))
	}

	log.Printf("[DEBUG] Creating VPC IPv4 CIDR block association: %#v", req)
	resp, err := conn.AssociateVpcCidrBlock(req)
	if err != nil {
		return fmt.Errorf("Error creating VPC IPv4 CIDR block association: %s", err)
	}

	d.SetId(aws.StringValue(resp.CidrBlockAssociation.AssociationId))

	stateConf := &resource.StateChangeConf{
		Pending:    []string{ec2.VpcCidrBlockStateCodeAssociating},
		Target:     []string{ec2.VpcCidrBlockStateCodeAssociated},
		Refresh:    vpcIpv4CidrBlockAssociationStateRefresh(conn, d.Get("vpc_id").(string), d.Id()),
		Timeout:    d.Timeout(schema.TimeoutCreate),
		Delay:      10 * time.Second,
		MinTimeout: 5 * time.Second,
	}
	_, err = stateConf.WaitForState()
	if err != nil {
		return fmt.Errorf("Error waiting for IPv4 CIDR block association (%s) to become available: %s", d.Id(), err)
	}

	return resourceVPCIPv4CIDRBlockAssociationRead(d, meta)
}

func resourceVPCIPv4CIDRBlockAssociationRead(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*conns.AWSClient).EC2Conn

	input := &ec2.DescribeVpcsInput{
		Filters: BuildAttributeFilterList(
			map[string]string{
				"cidr-block-association.association-id": d.Id(),
			},
		),
	}

	log.Printf("[DEBUG] Describing VPCs: %s", input)
	output, err := conn.DescribeVpcs(input)
	if err != nil {
		return fmt.Errorf("error describing VPCs: %s", err)
	}

	if output == nil || len(output.Vpcs) == 0 || output.Vpcs[0] == nil {
		log.Printf("[WARN] IPv4 CIDR block association (%s) not found, removing from state", d.Id())
		d.SetId("")
		return nil
	}

	vpc := output.Vpcs[0]

	var vpcCidrBlockAssociation *ec2.VpcCidrBlockAssociation
	for _, cidrBlockAssociation := range vpc.CidrBlockAssociationSet {
		if aws.StringValue(cidrBlockAssociation.AssociationId) == d.Id() {
			vpcCidrBlockAssociation = cidrBlockAssociation
			break
		}
	}

	if vpcCidrBlockAssociation == nil {
		log.Printf("[WARN] IPv4 CIDR block association (%s) not found, removing from state", d.Id())
		d.SetId("")
		return nil
	}

	d.Set("cidr_block", vpcCidrBlockAssociation.CidrBlock)
	d.Set("vpc_id", vpc.VpcId)

	return nil
}

func resourceVPCIPv4CIDRBlockAssociationDelete(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*conns.AWSClient).EC2Conn

	log.Printf("[DEBUG] Deleting VPC IPv4 CIDR block association: %s", d.Id())
	_, err := conn.DisassociateVpcCidrBlock(&ec2.DisassociateVpcCidrBlockInput{
		AssociationId: aws.String(d.Id()),
	})
	if err != nil {
		if tfawserr.ErrMessageContains(err, "InvalidVpcID.NotFound", "") {
			return nil
		}
		return fmt.Errorf("Error deleting VPC IPv4 CIDR block association: %s", err)
	}

	stateConf := &resource.StateChangeConf{
		Pending:    []string{ec2.VpcCidrBlockStateCodeDisassociating},
		Target:     []string{ec2.VpcCidrBlockStateCodeDisassociated, VpcCidrBlockStateCodeDeleted},
		Refresh:    vpcIpv4CidrBlockAssociationStateRefresh(conn, d.Get("vpc_id").(string), d.Id()),
		Timeout:    d.Timeout(schema.TimeoutDelete),
		Delay:      10 * time.Second,
		MinTimeout: 5 * time.Second,
	}
	_, err = stateConf.WaitForState()
	if err != nil {
		return fmt.Errorf("Error waiting for VPC IPv4 CIDR block association (%s) to be deleted: %s", d.Id(), err.Error())
	}

	return nil
}

func vpcIpv4CidrBlockAssociationStateRefresh(conn *ec2.EC2, vpcId, assocId string) resource.StateRefreshFunc {
	return func() (interface{}, string, error) {
		vpc, err := vpcDescribe(conn, vpcId)
		if err != nil {
			return nil, "", err
		}

		if vpc != nil {
			for _, cidrAssociation := range vpc.CidrBlockAssociationSet {
				if aws.StringValue(cidrAssociation.AssociationId) == assocId {
					return cidrAssociation, aws.StringValue(cidrAssociation.CidrBlockState.State), nil
				}
			}
		}

		return "", VpcCidrBlockStateCodeDeleted, nil
	}
}
