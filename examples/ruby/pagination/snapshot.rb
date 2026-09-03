# frozen_string_literal: true

require 'northrays'

northrays = Northrays::Northrays.new

result = northrays.snapshot.list(page: 2, limit: 10)
result.items.each do |snapshot|
  puts "#{snapshot.name} (#{snapshot.image_name})"
end
