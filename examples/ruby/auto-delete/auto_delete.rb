# frozen_string_literal: true

require 'northrays'

northrays = Northrays::Northrays.new

# Auto delete disabled by default
first_sandbox = northrays.create
puts "Default auto delete interval: #{first_sandbox.auto_delete_interval}"

# Auto delete after the Sandbox has been stopped for 1 hour
first_sandbox.auto_delete_interval = 60
puts "Auto delete interval: #{first_sandbox.auto_delete_interval}"

# Delete immediately upon stopping
first_sandbox.auto_delete_interval = 0
puts "Auto delete interval: #{first_sandbox.auto_delete_interval}"

# Disable auto delete
first_sandbox.auto_delete_interval = -1
puts "Auto delete interval: #{first_sandbox.auto_delete_interval}"

# Auto delete after the Sandbox has been stopped for 1 day
second_sandbox = northrays.create(Northrays::CreateSandboxFromSnapshotParams.new(auto_delete_interval: 24 * 60))
puts "Auto delete interval: #{second_sandbox.auto_delete_interval}"

northrays.delete(first_sandbox)
northrays.delete(second_sandbox)
