// Copyright Daytona Platforms Inc.
// SPDX-License-Identifier: Apache-2.0

package io.northrays.sdk;

import io.northrays.api.client.api.VolumesApi;
import io.northrays.api.client.model.CreateVolume;
import io.northrays.sdk.model.Volume;

import java.util.List;
import java.util.ArrayList;

/**
 * Service for managing Northrays Volumes.
 *
 * <p>Volumes provide persistent shared storage that can be mounted into Sandboxes.
 */
public class VolumeService {
    private final VolumesApi volumesApi;

    VolumeService(VolumesApi volumesApi) {
        this.volumesApi = volumesApi;
    }

    /**
     * Creates a new volume.
     *
     * @param name volume name
     * @return created {@link Volume}
     * @throws io.northrays.sdk.exception.NorthraysException if creation fails
     */
    public Volume create(String name) {
        io.northrays.api.client.model.VolumeDto volumeDto = ExceptionMapper.callMain(
                () -> volumesApi.createVolume(new CreateVolume().name(name), null)
        );
        return toVolume(volumeDto);
    }

    /**
     * Lists all accessible volumes.
     *
     * @return list of available volumes
     * @throws io.northrays.sdk.exception.NorthraysException if the API request fails
     */
    public List<Volume> list() {
        List<io.northrays.api.client.model.VolumeDto> volumes = ExceptionMapper.callMain(() -> volumesApi.listVolumes(null, null));
        List<Volume> result = new ArrayList<Volume>();
        if (volumes != null) {
            for (io.northrays.api.client.model.VolumeDto volume : volumes) {
                result.add(toVolume(volume));
            }
        }
        return result;
    }

    /**
     * Retrieves a volume by name.
     *
     * @param name volume name
     * @return matching {@link Volume}
     * @throws io.northrays.sdk.exception.NorthraysException if no volume is found or request fails
     */
    public Volume getByName(String name) {
        io.northrays.api.client.model.VolumeDto volumeDto = ExceptionMapper.callMain(() -> volumesApi.getVolumeByName(name, null));
        return toVolume(volumeDto);
    }

    /**
     * Deletes a volume by ID.
     *
     * @param id volume identifier
     * @throws io.northrays.sdk.exception.NorthraysException if deletion fails
     */
    public void delete(String id) {
        ExceptionMapper.runMain(() -> volumesApi.deleteVolume(id, null));
    }

    private Volume toVolume(io.northrays.api.client.model.VolumeDto source) {
        Volume volume = new Volume();
        if (source != null) {
            volume.setId(source.getId());
            volume.setName(source.getName());
            volume.setState(source.getState() == null ? null : source.getState().getValue());
        }
        return volume;
    }
}
