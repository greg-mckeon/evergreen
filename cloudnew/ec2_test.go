package cloudnew

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/evergreen-ci/evergreen"
	"github.com/evergreen-ci/evergreen/cloud"
	"github.com/evergreen-ci/evergreen/db"
	"github.com/evergreen-ci/evergreen/model/distro"
	"github.com/evergreen-ci/evergreen/model/host"
	"github.com/evergreen-ci/evergreen/testutil"
	"github.com/stretchr/testify/suite"
)

type EC2Suite struct {
	suite.Suite
	opts *EC2ManagerOptions
	m    cloud.CloudManager
}

func TestEC2Suite(t *testing.T) {
	suite.Run(t, new(EC2Suite))
}

func (s *EC2Suite) SetupSuite() {
	db.SetGlobalSessionProvider(testutil.TestConfig().SessionFactory())
}

func (s *EC2Suite) SetupTest() {
	s.Require().NoError(db.Clear(host.Collection))
	s.opts = &EC2ManagerOptions{
		client: &AWSClientMock{},
	}
	s.m = NewEC2Manager(s.opts)
}

func (s *EC2Suite) TestConstructor() {
	s.Implements((*cloud.CloudManager)(nil), NewEC2Manager(s.opts))
}

func (s *EC2Suite) TestValidateProviderSettings() {
	p := &EC2ProviderSettings{
		AMI:           "ami",
		InstanceType:  "type",
		SecurityGroup: "sg-123456",
		KeyName:       "keyName",
	}
	s.Nil(p.Validate())
	p.AMI = ""
	s.Error(p.Validate())
	p.AMI = "ami"

	s.Nil(p.Validate())
	p.InstanceType = ""
	s.Error(p.Validate())
	p.InstanceType = "type"

	s.Nil(p.Validate())
	p.SecurityGroup = ""
	s.Error(p.Validate())
	p.SecurityGroup = "sg-123456"

	s.Nil(p.Validate())
	p.KeyName = ""
	s.Error(p.Validate())
}

func (s *EC2Suite) TestMakeDeviceMappings() {
	validMount := cloud.MountPoint{
		DeviceName:  "device",
		VirtualName: "virtual",
	}

	m := []cloud.MountPoint{}
	b, err := makeBlockDeviceMappings(m)
	s.NoError(err)
	s.Len(b, 0)

	noDeviceName := validMount
	noDeviceName.DeviceName = ""
	m = []cloud.MountPoint{validMount, noDeviceName}
	b, err = makeBlockDeviceMappings(m)
	s.Nil(b)
	s.Error(err)

	noVirtualName := validMount
	noVirtualName.VirtualName = ""
	m = []cloud.MountPoint{validMount, noVirtualName}
	b, err = makeBlockDeviceMappings(m)
	s.Nil(b)
	s.Error(err)

	anotherMount := validMount
	anotherMount.DeviceName = "anotherDeviceName"
	anotherMount.VirtualName = "anotherVirtualName"
	m = []cloud.MountPoint{validMount, anotherMount}
	b, err = makeBlockDeviceMappings(m)
	s.Len(b, 2)
	s.Equal("device", *b[0].DeviceName)
	s.Equal("virtual", *b[0].VirtualName)
	s.Equal("anotherDeviceName", *b[1].DeviceName)
	s.Equal("anotherVirtualName", *b[1].VirtualName)
	s.NoError(err)
}

func (s *EC2Suite) TestGetSettings() {
	s.Equal(&EC2ProviderSettings{}, s.m.GetSettings())
}

func (s *EC2Suite) TestConfigure() {
	settings := &evergreen.Settings{}
	err := s.m.Configure(settings)
	s.Error(err)

	settings.Providers.AWS.Id = "id"
	err = s.m.Configure(settings)
	s.Error(err)

	settings.Providers.AWS.Id = ""
	settings.Providers.AWS.Secret = "secret"
	err = s.m.Configure(settings)
	s.Error(err)

	settings.Providers.AWS.Id = "id"
	err = s.m.Configure(settings)
	s.NoError(err)
	ec2m := s.m.(*ec2Manager)
	creds, err := ec2m.credentials.Get()
	s.NoError(err)
	s.Equal("id", creds.AccessKeyID)
	s.Equal("secret", creds.SecretAccessKey)
}

func (s *EC2Suite) TestSpawnHostInvalidInput() {
	h := &host.Host{
		Distro: distro.Distro{
			Provider: "foo",
			Id:       "id",
		},
	}
	spawned, err := s.m.SpawnHost(h)
	s.Nil(spawned)
	s.Error(err)
	s.EqualError(err, "Can't spawn instance of ec2 for distro id: provider is foo")

	h.Distro.Provider = evergreen.ProviderNameEc2OnDemand
	spawned, err = s.m.SpawnHost(h)
	s.Nil(spawned)
	s.Error(err)
}

func (s *EC2Suite) TestSpawnHostClassic() {
	h := &host.Host{}
	h.Distro.Id = "distro_id"
	h.Distro.Provider = evergreen.ProviderNameEc2OnDemand
	h.Distro.ProviderSettings = &map[string]interface{}{
		"ami":           "ami",
		"instance_type": "instanceType",
		"key_name":      "keyName",
		"mount_points": []map[string]string{
			map[string]string{"device_name": "device", "virtual_name": "virtual"},
		},
		"security_group": "sg-123456",
		"subnet_id":      "subnet-123456",
	}

	_, err := s.m.SpawnHost(h)
	s.NoError(err)

	manager, ok := s.m.(*ec2Manager)
	s.True(ok)
	mock, ok := manager.client.(*AWSClientMock)
	s.True(ok)

	fmt.Println(mock.RunInstancesInput)
	runInput := *mock.RunInstancesInput
	s.Equal("ami", *runInput.ImageId)
	s.Equal("instanceType", *runInput.InstanceType)
	s.Equal("keyName", *runInput.KeyName)
	s.Equal("virtual", *runInput.BlockDeviceMappings[0].VirtualName)
	s.Equal("device", *runInput.BlockDeviceMappings[0].DeviceName)
	s.Equal("sg-123456", *runInput.SecurityGroups[0])
	s.Nil(runInput.SecurityGroupIds)
	s.Nil(runInput.SubnetId)
	describeInput := *mock.DescribeInstancesInput
	s.Equal("instance_id", *describeInput.InstanceIds[0])
	tagsInput := *mock.CreateTagsInput
	s.Equal("instance_id", *tagsInput.Resources[0])
	s.Len(tagsInput.Tags, 8)
	var foundInstanceName bool
	var foundDistroID bool
	for _, tag := range tagsInput.Tags {
		if *tag.Key == "name" {
			foundInstanceName = true
			s.Equal(*tag.Value, "instance_id")
		}
		if *tag.Key == "distro" {
			foundDistroID = true
			s.Equal(*tag.Value, "distro_id")
		}
	}
	s.True(foundInstanceName)
	s.True(foundDistroID)
}

func (s *EC2Suite) TestSpawnHostVPC() {
	h := &host.Host{}
	h.Distro.Id = "distro_id"
	h.Distro.Provider = evergreen.ProviderNameEc2OnDemand
	h.Distro.ProviderSettings = &map[string]interface{}{
		"ami":           "ami",
		"instance_type": "instanceType",
		"key_name":      "keyName",
		"mount_points": []map[string]string{
			map[string]string{"device_name": "device", "virtual_name": "virtual"},
		},
		"security_group": "sg-123456",
		"subnet_id":      "subnet-123456",
		"is_vpc":         true,
	}

	_, err := s.m.SpawnHost(h)
	s.NoError(err)

	manager, ok := s.m.(*ec2Manager)
	s.True(ok)
	mock, ok := manager.client.(*AWSClientMock)
	s.True(ok)

	fmt.Println(mock.RunInstancesInput)
	runInput := *mock.RunInstancesInput
	s.Equal("ami", *runInput.ImageId)
	s.Equal("instanceType", *runInput.InstanceType)
	s.Equal("keyName", *runInput.KeyName)
	s.Equal("virtual", *runInput.BlockDeviceMappings[0].VirtualName)
	s.Equal("device", *runInput.BlockDeviceMappings[0].DeviceName)
	s.Equal("sg-123456", *runInput.SecurityGroupIds[0])
	s.Nil(runInput.SecurityGroups)
	s.Equal("subnet-123456", *runInput.SubnetId)
	describeInput := *mock.DescribeInstancesInput
	s.Equal("instance_id", *describeInput.InstanceIds[0])
	tagsInput := *mock.CreateTagsInput
	s.Equal("instance_id", *tagsInput.Resources[0])
	s.Len(tagsInput.Tags, 8)
	var foundInstanceName bool
	var foundDistroID bool
	for _, tag := range tagsInput.Tags {
		if *tag.Key == "name" {
			foundInstanceName = true
			s.Equal(*tag.Value, "instance_id")
		}
		if *tag.Key == "distro" {
			foundDistroID = true
			s.Equal(*tag.Value, "distro_id")
		}
	}
	s.True(foundInstanceName)
	s.True(foundDistroID)
}

func (s *EC2Suite) TestCanSpawn() {
	can, err := s.m.CanSpawn()
	s.True(can)
	s.NoError(err)
}

func (s *EC2Suite) TestGetInstanceStatus() {
	status, err := s.m.GetInstanceStatus(&host.Host{})
	s.NoError(err)
	s.Equal(cloud.StatusRunning, status)
}

func (s *EC2Suite) TestTerminateInstance() {
	h := &host.Host{Id: "host_id"}
	s.NoError(h.Insert())
	s.NoError(s.m.TerminateInstance(h))
	found, err := host.FindOne(host.ById("host_id"))
	s.Equal(evergreen.HostTerminated, found.Status)
	s.NoError(err)
}

func (s *EC2Suite) TestIsUp() {
	up, err := s.m.IsUp(&host.Host{})
	s.True(up)
	s.NoError(err)
}

func (s *EC2Suite) TestOnUp() {
	s.NoError(s.m.OnUp(nil))
}

func (s *EC2Suite) TestGetDNSName() {
	dns, err := s.m.GetDNSName(&host.Host{})
	s.Equal("public_dns_name", dns)
	s.NoError(err)
}

func (s *EC2Suite) TestGetSSHOptionsEmptyKey() {
	opts, err := s.m.GetSSHOptions(&host.Host{}, "")
	s.Nil(opts)
	s.Error(err)
}

func (s *EC2Suite) TestGetSSHOptions() {
	h := &host.Host{
		Distro: distro.Distro{
			SSHOptions: []string{
				"foo",
				"bar",
			},
		},
	}
	opts, err := s.m.GetSSHOptions(h, "key")
	s.Equal([]string{"-i", "key", "-o", "foo", "-o", "bar", "-o", "UserKnownHostsFile=/dev/null"}, opts)
	s.NoError(err)
}

func (s *EC2Suite) TestTimeTilNextPaymentLinux() {
	h := &host.Host{
		Distro: distro.Distro{
			Arch: "linux",
		},
	}
	s.Equal(time.Second, s.m.TimeTilNextPayment(h))
}

func (s *EC2Suite) TestTimeTilNextPaymentWindows() {
	now := time.Now()
	thirtyMinutesAgo := now.Add(-30 * time.Minute)
	h := &host.Host{
		Distro: distro.Distro{
			Arch: "windows",
		},
		CreationTime: thirtyMinutesAgo,
		StartTime:    thirtyMinutesAgo.Add(time.Minute),
	}
	s.InDelta(31*time.Minute, s.m.TimeTilNextPayment(h), float64(time.Millisecond))
}

func (s *EC2Suite) TestGetInstanceName() {
	id := s.m.GetInstanceName(&distro.Distro{Id: "foo"})
	fmt.Printf("the id is %s\n", id)
	s.True(strings.HasPrefix(id, "evg-foo-"))
}
