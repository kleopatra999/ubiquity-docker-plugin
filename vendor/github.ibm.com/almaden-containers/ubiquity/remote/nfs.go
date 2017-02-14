package remote

import (
	"fmt"
	"log"

	"net/http"
	"os/exec"
	"path"
	"strings"

	"github.ibm.com/almaden-containers/ubiquity/resources"
	"github.ibm.com/almaden-containers/ubiquity/utils"
)

type nfsRemoteClient struct {
	logger        *log.Logger
	isActivated   bool
	httpClient    *http.Client
	storageApiURL string
	backendName   string
	config        resources.SpectrumNfsRemoteConfig
}

func NewNfsRemoteClient(logger *log.Logger, backendName, storageApiURL string, config resources.SpectrumNfsRemoteConfig) (resources.StorageClient, error) {
	if config.ClientConfig == "" {
		return nil, fmt.Errorf("newNFSRemoteClient: Missing required parameter 'clientConfig'")
	}
	return &nfsRemoteClient{logger: logger, storageApiURL: storageApiURL, httpClient: &http.Client{}, backendName: backendName, config: config}, nil
}

func (s *nfsRemoteClient) Activate() error {
	s.logger.Println("nfsRemoteClient: Activate start")
	defer s.logger.Println("nfsRemoteClient: Activate end")

	if s.isActivated {
		return nil
	}

	// call remote activate
	activateURL := utils.FormatURL(s.storageApiURL, s.backendName, "activate")
	response, err := utils.HttpExecute(s.httpClient, s.logger, "POST", activateURL, nil)
	if err != nil {
		s.logger.Printf("nfsRemoteClient: Error in activate remote call %#v", err)
		return fmt.Errorf("Error in activate remote call")
	}

	if response.StatusCode != http.StatusOK {
		s.logger.Printf("nfsRemoteClient: Error in activate remote call %#v\n", response)
		return utils.ExtractErrorResponse(response)
	}
	s.logger.Println("nfsRemoteClient: Activate success")
	s.isActivated = true
	return nil
}

func (s *nfsRemoteClient) CreateVolume(name string, opts map[string]interface{}) (err error) {
	s.logger.Println("nfsRemoteClient: create start")
	defer s.logger.Println("nfsRemoteClient: create end")

	createRemoteURL := utils.FormatURL(s.storageApiURL, s.backendName, "volumes")

	extendedOpts := make(map[string]interface{})
	for k, v := range opts {
		extendedOpts[k] = v
	}
	extendedOpts["nfsClientConfig"] = s.config.ClientConfig
	createVolumeRequest := resources.CreateRequest{Name: name, Opts: extendedOpts}

	response, err := utils.HttpExecute(s.httpClient, s.logger, "POST", createRemoteURL, createVolumeRequest)
	if err != nil {
		s.logger.Printf("nfsRemoteClient: Error in create volume remote call %#v", err)
		return fmt.Errorf("Error in create volume remote call")
	}

	if response.StatusCode != http.StatusOK {
		s.logger.Printf("nfsRemoteClient: Error in create volume remote call %#v", response)
		return utils.ExtractErrorResponse(response)
	}

	return nil
}

func (s *nfsRemoteClient) RemoveVolume(name string, forceDelete bool) (err error) {
	s.logger.Println("nfsRemoteClient: remove start")
	defer s.logger.Println("nfsRemoteClient:  remove end")

	removeRemoteURL := utils.FormatURL(s.storageApiURL, s.backendName, "volumes", name)
	removeRequest := resources.RemoveRequest{Name: name, ForceDelete: forceDelete}

	response, err := utils.HttpExecute(s.httpClient, s.logger, "DELETE", removeRemoteURL, removeRequest)
	if err != nil {
		s.logger.Printf("nfsRemoteClient: Error in remove volume remote call %#v", err)
		return fmt.Errorf("Error in remove volume remote call")
	}

	if response.StatusCode != http.StatusOK {
		s.logger.Printf("nfsRemoteClient: Error in remove volume remote call %#v", response)
		return utils.ExtractErrorResponse(response)
	}

	return nil
}

func (s *nfsRemoteClient) GetVolume(name string) (resources.VolumeMetadata, map[string]interface{}, error) {
	s.logger.Println("nfsRemoteClient: get start")
	defer s.logger.Println("nfsRemoteClient: get finish")

	getRemoteURL := utils.FormatURL(s.storageApiURL, s.backendName, "volumes", name)
	response, err := utils.HttpExecute(s.httpClient, s.logger, "GET", getRemoteURL, nil)
	if err != nil {
		s.logger.Printf("nfsRemoteClient: Error in get volume remote call %#v", err)
		return resources.VolumeMetadata{}, nil, fmt.Errorf("Error in get volume remote call")
	}

	if response.StatusCode != http.StatusOK {
		s.logger.Printf("nfsRemoteClient: Error in get volume remote call %#v", response)
		return resources.VolumeMetadata{}, nil, utils.ExtractErrorResponse(response)
	}

	getResponse := resources.GetResponse{}
	err = utils.UnmarshalResponse(response, &getResponse)
	if err != nil {
		s.logger.Printf("nfsRemoteClient: Error in unmarshalling response for get remote call %#v for response %#v", err, response)
		return resources.VolumeMetadata{}, nil, fmt.Errorf("Error in unmarshalling response for get remote call")
	}

	return getResponse.Volume, getResponse.Config, nil
}

func (s *nfsRemoteClient) Attach(name string) (string, error) {
	s.logger.Println("nfsRemoteClient: attach start")
	defer s.logger.Println("nfsRemoteClient: attach end")

	attachRemoteURL := utils.FormatURL(s.storageApiURL, s.backendName, "volumes", name, "attach")
	response, err := utils.HttpExecute(s.httpClient, s.logger, "PUT", attachRemoteURL, nil)
	if err != nil {
		s.logger.Printf("nfsRemoteClient: Error in attach volume remote call %#v", err)
		return "", fmt.Errorf("Error in attach volume remote call")
	}

	if response.StatusCode != http.StatusOK {
		s.logger.Printf("nfsRemoteClient: Error in attach volume remote call %#v", response)

		return "", utils.ExtractErrorResponse(response)
	}

	// FIXME: Ubiquity Storage API should not return a MountResponse on Attach calls.
	mountResponse := resources.MountResponse{}
	err = utils.UnmarshalResponse(response, &mountResponse)
	if err != nil {
		return "", fmt.Errorf("Error in unmarshalling response for attach remote call")
	}

	nfsShare := mountResponse.Mountpoint
	// FIXME: What is our local mount path? Should we be getting this from the volume config? Using same path as on ubiquity server below /mnt/ for now.
	remoteMountpoint := path.Join("/mnt/", strings.Split(nfsShare, ":")[1])

	_, volumeConfig, err := s.GetVolume(name)
	if err != nil {
		return "", err
	}

	if s.isMounted(nfsShare, remoteMountpoint) {
		s.logger.Printf("nfsRemoteClient: - mount: %s is already mounted at %s\n", nfsShare, remoteMountpoint)
		return remoteMountpoint, nil
	}

	s.logger.Printf("nfsRemoteClient: mkdir -p %s\n", remoteMountpoint)
	args := []string{"mkdir", "-p", remoteMountpoint}

	executor := utils.NewExecutor(s.logger)
	_, err = executor.Execute("sudo", args)
	if err != nil {
		return "", fmt.Errorf("nfsRemoteClient: Failed to mkdir for remote mountpoint %s (share %s, error '%s')\n", remoteMountpoint, nfsShare, err.Error())
	}

	isPreexisting, isPreexistingSpecified := volumeConfig["isPreexisting"]
	if isPreexistingSpecified && isPreexisting.(bool) == false {
		uid, uidSpecified := volumeConfig["uid"]
		gid, gidSpecified := volumeConfig["gid"]
		executor := utils.NewExecutor(s.logger)
		if uidSpecified || gidSpecified {
			args := []string{"chown", fmt.Sprintf("%s:%s", uid, gid), remoteMountpoint}
			_, err = executor.Execute("sudo", args)
			if err != nil {
				s.logger.Printf("Failed to change permissions of mountpoint %s: %s", mountResponse.Mountpoint, err.Error())
				return "", err
			}
		} else {
			//chmod 777 mountpoint
			args := []string{"chmod", "777", remoteMountpoint}
			_, err = executor.Execute("sudo", args)
			if err != nil {
				s.logger.Printf("Failed to change permissions of mountpoint %s: %s", mountResponse.Mountpoint, err.Error())
				return "", err
			}
		}
	}
	return s.mount(nfsShare, remoteMountpoint)
}

func (s *nfsRemoteClient) Detach(name string) error {
	s.logger.Println("nfsRemoteClient: detach start")
	defer s.logger.Println("nfsRemoteClient: detach end")

	// FIXME: Use GetVolume to get mountpoint/nfs_share info (StorageClient.Detach does not have any response parameters)
	s.logger.Println("nfsRemoteClient: getting volume config for remote unmount")
	_, volumeConfig, err := s.GetVolume(name)
	if err != nil {
		return err
	}
	nfs_share := volumeConfig["nfs_share"].(string)

	// FIXME: What should be the local mount path? Should we be getting this from the volume config? Using same path as on ubiquity server below /mnt/ for now.
	remoteMountpoint := path.Join("/mnt/", strings.Split(nfs_share, ":")[1])

	if err := s.unmount(remoteMountpoint); err != nil {
		return err
	}

	detachRemoteURL := utils.FormatURL(s.storageApiURL, s.backendName, "volumes", name, "detach")
	response, err := utils.HttpExecute(s.httpClient, s.logger, "PUT", detachRemoteURL, nil)
	if err != nil {
		s.logger.Printf("nfsRemoteClient: Error in detach volume remote call %#v", err)
		return fmt.Errorf("Error in detach volume remote call")
	}

	if response.StatusCode != http.StatusOK {
		s.logger.Printf("nfsRemoteClient: Error in detach volume remote call %#v", response)
		return utils.ExtractErrorResponse(response)
	}

	return nil
}

func (s *nfsRemoteClient) ListVolumes() ([]resources.VolumeMetadata, error) {
	s.logger.Println("nfsRemoteClient: list start")
	defer s.logger.Println("nfsRemoteClient: list end")

	listRemoteURL := utils.FormatURL(s.storageApiURL, s.backendName, "volumes")
	response, err := utils.HttpExecute(s.httpClient, s.logger, "GET", listRemoteURL, nil)
	if err != nil {
		s.logger.Printf("nfsRemoteClient: Error in list volume remote call %#v", err)
		return nil, fmt.Errorf("Error in list volume remote call")
	}

	if response.StatusCode != http.StatusOK {
		s.logger.Printf("nfsRemoteClient: Error in list volume remote call %#v", err)
		return nil, utils.ExtractErrorResponse(response)
	}

	listResponse := resources.ListResponse{}
	err = utils.UnmarshalResponse(response, &listResponse)
	if err != nil {
		s.logger.Printf("nfsRemoteClient: Error in unmarshalling response for get remote call %#v for response %#v", err, response)
		return []resources.VolumeMetadata{}, nil
	}

	return listResponse.Volumes, nil

}

func (s *nfsRemoteClient) mount(nfsShare, remoteMountpoint string) (string, error) {
	s.logger.Printf("nfsRemoteClient: - mount start nfsShare=%s\n", nfsShare)
	defer s.logger.Printf("nfsRemoteClient: - mount end nfsShare=%s\n", nfsShare)

	executor := utils.NewExecutor(s.logger)
	args := []string{"mount", "-t", "nfs", nfsShare, remoteMountpoint}
	output, err := executor.Execute("sudo", args)
	if err != nil {
		return "", fmt.Errorf("nfsRemoteClient: Failed to mount share %s to remote mountpoint %s (error '%s', output '%s')\n", nfsShare, remoteMountpoint, err.Error(), output)
	}
	s.logger.Printf("nfsRemoteClient:  mount output: %s\n", string(output))

	return remoteMountpoint, nil
}

func (s *nfsRemoteClient) isMounted(nfsShare, remoteMountpoint string) bool {
	command := "grep"
	args := []string{"-qs", fmt.Sprintf("%s\\s%s", nfsShare, remoteMountpoint), "/proc/mounts"}
	cmd := exec.Command(command, args...)
	_, err := cmd.Output()
	if err != nil {
		return false
	}
	return true
}

func (s *nfsRemoteClient) unmount(remoteMountpoint string) error {
	s.logger.Printf("nfsRemoteClient: - unmount start remoteMountpoint=%s\n", remoteMountpoint)
	defer s.logger.Printf("nfsRemoteClient: - unmount end remoteMountpoint=%s\n", remoteMountpoint)
	executor := utils.NewExecutor(s.logger)
	args := []string{"umount", remoteMountpoint}
	output, err := executor.Execute("sudo", args)
	if err != nil {
		return fmt.Errorf("Failed to unmount remote mountpoint %s (error '%s', output '%s')\n", remoteMountpoint, err.Error(), output)
	}
	s.logger.Printf("nfsRemoteClient: umount output: %s\n", string(output))

	return nil
}
