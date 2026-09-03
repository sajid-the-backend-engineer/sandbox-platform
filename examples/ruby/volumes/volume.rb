# frozen_string_literal: true

require 'northrays'

northrays = Northrays::Northrays.new

# Create a new volume or get an existing one
volume = northrays.volume.get('my-volume', create: true)

# Mount the volume to the sandbox
mount_dir = '/home/northrays/volume'

sandbox = northrays.create(
  Northrays::CreateSandboxFromSnapshotParams.new(
    language: Northrays::CodeLanguage::PYTHON,
    volumes: [NorthraysApiClient::SandboxVolume.new(volume_id: volume.id, mount_path: mount_dir)]
  )
)

# Create a new directory in the mount directory
new_dir = File.expand_path('new-dir', mount_dir)
sandbox.fs.create_folder(new_dir, '755')

# Create a new file in the mount directory
new_file = File.expand_path('new-file.txt', mount_dir)
sandbox.fs.upload_file('Hello world', new_file)

# List files in the mount directory
files = sandbox.fs.list_files(mount_dir)
puts "Files: #{files}"

# Create a new sandbox with the same volume
# and mount it to the different path
other_dir = '/home/northrays/my-files'
other_sandbox = northrays.create(
  Northrays::CreateSandboxFromSnapshotParams.new(
    language: Northrays::CodeLanguage::PYTHON,
    volumes: [NorthraysApiClient::SandboxVolume.new(volume_id: volume.id, mount_path: other_dir)]
  )
)

# List files in the mount directory
files = other_sandbox.fs.list_files(other_dir)
puts "Files: #{files}"

# Get the file from the mount directory
file = other_sandbox.fs.download_file(File.expand_path('new-file.txt', other_dir))
puts "File: #{file}"

# Cleanup
northrays.delete(sandbox)
northrays.delete(other_sandbox)
northrays.volume.delete(volume)
