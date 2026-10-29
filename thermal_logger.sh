```sh
#!/bin/sh
# Read Mono Gateway sensors and output JSONL or send readings to a host.
#
# Usage:
#   sh thermal_push.sh             # Send JSON readings to HOST
#   sh thermal_push.sh -p          # Print JSONL to stdout
#   sh thermal_push.sh -p > sensors.jsonl
#
# Optional environment variables:
#   HOST=192.168.1.10:9999
#   DIAG_TOKEN=your-shared-secret
#   INTERVAL=1
#
# HTTP mode expects mono-diagnostics-collector:
#   POST http://HOST/d
#   Header: X-Diag-Token: DIAG_TOKEN
#   Content-Type: application/json
#
# Each JSONL record contains a UTC timestamp and all sensor readings.
# Unavailable or invalid readings are represented as JSON null.

HOST=${HOST:-192.168.1.10:9999}
INTERVAL=${INTERVAL:-1}
DIAG_TOKEN=${DIAG_TOKEN:-}
PRINT_ONLY=0

while getopts ":ph" opt; do
  case "$opt" in
    p) PRINT_ONLY=1 ;;
    h)
      echo "Usage: $0 [-p]"
      exit 0
      ;;
    :)
      echo "Option -$OPTARG requires an argument" >&2
      exit 2
      ;;
    \?)
      echo "Usage: $0 [-p]" >&2
      exit 2
      ;;
  esac
done
shift $((OPTIND - 1))

if [ "$#" -ne 0 ]; then
  echo "Usage: $0 [-p]" >&2
  exit 2
fi

if [ "$PRINT_ONLY" -eq 0 ] && [ -z "$DIAG_TOKEN" ]; then
  echo "Error: DIAG_TOKEN is required for HTTP mode" >&2
  exit 1
fi

# Convert sysfs millidegrees Celsius to degrees Celsius.
to_celsius() {
  awk -v value="$1" '
    BEGIN {
      if (value ~ /^-?[0-9]+$/)
        printf "%.3f", value / 1000
      else
        printf "N/A"
    }
  '
}

# Convert sysfs microwatts to watts.
to_watts() {
  awk -v value="$1" '
    BEGIN {
      if (value ~ /^-?[0-9]+$/)
        printf "%.3f", value / 1000000
      else
        printf "N/A"
    }
  '
}

# Print a non-negative integer, or N/A if missing or invalid.
to_number_or_na() {
  case "$1" in
    ''|*[!0-9]*) printf 'N/A' ;;
    *) printf '%s' "$1" ;;
  esac
}

# Convert a numeric reading to a JSON number.
# Missing or invalid values become JSON null.
json_number() {
  awk -v value="$1" '
    BEGIN {
      if (value ~ /^-?[0-9]+([.][0-9]+)?$/)
        printf "%s", value
      else
        printf "null"
    }
  '
}

# Find a hwmon attribute by hwmon name.
hwmon_value_by_name() {
  wanted_name=$1
  attribute=$2

  for hwmon in /sys/class/hwmon/hwmon*; do
    [ -r "$hwmon/name" ] || continue
    [ "$(cat "$hwmon/name" 2>/dev/null)" = "$wanted_name" ] || continue
    [ -r "$hwmon/$attribute" ] || continue
    cat "$hwmon/$attribute" 2>/dev/null
    return 0
  done

  return 1
}

# Find a hwmon attribute by its label file.
hwmon_value_by_label() {
  wanted_label=$1
  attribute=$2

  for hwmon in /sys/class/hwmon/hwmon*; do
    [ -r "$hwmon/label" ] || continue
    [ "$(cat "$hwmon/label" 2>/dev/null)" = "$wanted_label" ] || continue
    [ -r "$hwmon/$attribute" ] || continue
    cat "$hwmon/$attribute" 2>/dev/null
    return 0
  done

  return 1
}

# Find a hwmon attribute by the ending of its device-tree node path.
hwmon_value_by_of_suffix() {
  suffix=$1
  attribute=$2

  for hwmon in /sys/class/hwmon/hwmon*; do
    [ -r "$hwmon/$attribute" ] || continue

    node=$(readlink -f "$hwmon/device/of_node" 2>/dev/null)
    case "$node" in
      *"$suffix")
        cat "$hwmon/$attribute" 2>/dev/null
        return 0
        ;;
    esac
  done

  return 1
}

# Return success if this hwmon device identifies as the requested XFI cage.
hwmon_matches_xfi() {
  sensor_dir=$1
  xfi_id=$2

  sensor_name=$(cat "$sensor_dir/name" 2>/dev/null)
  device_path=$(readlink -f "$sensor_dir/device" 2>/dev/null)
  of_path=$(readlink -f "$sensor_dir/device/of_node" 2>/dev/null)

  case "$xfi_id" in
    xfi0)
      case "$sensor_name" in
        sfp_xfi0|xfi0) return 0 ;;
      esac
      case "$device_path:$of_path" in
        *sfp_xfi0*|*'/xfi0:'*|*ethernet@f0000*) return 0 ;;
      esac
      ;;
    xfi1)
      case "$sensor_name" in
        sfp_xfi1|xfi1) return 0 ;;
      esac
      case "$device_path:$of_path" in
        *sfp_xfi1*|*'/xfi1:'*|*ethernet@f2000*) return 0 ;;
      esac
      ;;
  esac

  return 1
}

# Find an XFI hwmon attribute by sensor name or device path.
hwmon_value_by_xfi() {
  requested_xfi=$1
  requested_attribute=$2

  for hwmon in /sys/class/hwmon/hwmon*; do
    [ -r "$hwmon/$requested_attribute" ] || continue

    if hwmon_matches_xfi "$hwmon" "$requested_xfi"; then
      cat "$hwmon/$requested_attribute" 2>/dev/null
      return 0
    fi
  done

  return 1
}

while :; do
  timestamp=$(date -u '+%Y-%m-%dT%H:%M:%SZ')

  # LS1046A thermal zones; values are millidegrees Celsius.
  ddr=$(to_celsius "$(hwmon_value_by_name ddr_thermal temp1_input)")
  serdes=$(to_celsius "$(hwmon_value_by_name serdes_thermal temp1_input)")
  fman=$(to_celsius "$(hwmon_value_by_name fman_thermal temp1_input)")
  cluster=$(to_celsius "$(hwmon_value_by_name cluster_thermal temp1_input)")
  sec=$(to_celsius "$(hwmon_value_by_name sec_thermal temp1_input)")

  # GPY PHY temperatures, matched by device-tree PHY address.
  gpy_u11000=$(to_celsius "$(hwmon_value_by_of_suffix '/ethernet-phy@0' temp1_input)")
  gpy_u12000=$(to_celsius "$(hwmon_value_by_of_suffix '/ethernet-phy@1' temp1_input)")
  gpy_u13000=$(to_celsius "$(hwmon_value_by_of_suffix '/ethernet-phy@2' temp1_input)")

  # TMP431 sensors on I2C mux channels 1 and 2.
  # temp1 is local; temp2 is the remote diode reading.
  tmp_u58000_local=$(to_celsius "$(hwmon_value_by_of_suffix '/i2c@1/temp-sensor@4c' temp1_input)")
  tmp_u58000_remote=$(to_celsius "$(hwmon_value_by_of_suffix '/i2c@1/temp-sensor@4c' temp2_input)")
  tmp_u58001_local=$(to_celsius "$(hwmon_value_by_of_suffix '/i2c@2/temp-sensor@4c' temp1_input)")
  tmp_u58001_remote=$(to_celsius "$(hwmon_value_by_of_suffix '/i2c@2/temp-sensor@4c' temp2_input)")

  # SFP+ XFI module temperatures.
  # In the board DTS, XFI0 is P16000 and XFI1 is P17000.
  sfp_p16000=$(to_celsius "$(hwmon_value_by_xfi xfi0 temp1_input)")
  sfp_p17000=$(to_celsius "$(hwmon_value_by_xfi xfi1 temp1_input)")

  # Cooling fan (EMC2305): tachometer in RPM and PWM duty (0-255).
  fan1_rpm=$(to_number_or_na "$(hwmon_value_by_name emc2305 fan1_input)")
  fan1_pwm=$(to_number_or_na "$(hwmon_value_by_name emc2305 pwm1)")

  # INA234 power monitor labelled "USB Power Delivery".
  # Sysfs power value is in microwatts.
  usb_pd_w=$(to_watts "$(hwmon_value_by_label 'USB Power Delivery' power1_input)")

  # Construct one JSON object. Every sensor reading is a JSON number or null.
  payload=$(printf \
    '{"timestamp":"%s","ls1046a_ddr_controller_c":%s,"ls1046a_serdes_c":%s,"ls1046a_fman_c":%s,"ls1046a_a72_cluster_c":%s,"ls1046a_sec_c":%s,"gpy_u11000_c":%s,"gpy_u12000_c":%s,"gpy_u13000_c":%s,"tmp_u58000_local_c":%s,"tmp_u58000_remote_c":%s,"tmp_u58001_local_c":%s,"tmp_u58001_remote_c":%s,"sfp_p16000_c":%s,"sfp_p17000_c":%s,"fan1_rpm":%s,"fan1_pwm":%s,"usb_pd_w":%s}' \
    "$timestamp" \
    "$(json_number "$ddr")" \
    "$(json_number "$serdes")" \
    "$(json_number "$fman")" \
    "$(json_number "$cluster")" \
    "$(json_number "$sec")" \
    "$(json_number "$gpy_u11000")" \
    "$(json_number "$gpy_u12000")" \
    "$(json_number "$gpy_u13000")" \
    "$(json_number "$tmp_u58000_local")" \
    "$(json_number "$tmp_u58000_remote")" \
    "$(json_number "$tmp_u58001_local")" \
    "$(json_number "$tmp_u58001_remote")" \
    "$(json_number "$sfp_p16000")" \
    "$(json_number "$sfp_p17000")" \
    "$(json_number "$fan1_rpm")" \
    "$(json_number "$fan1_pwm")" \
    "$(json_number "$usb_pd_w")")

  if [ "$PRINT_ONLY" -eq 1 ]; then
    # One JSON object per line, with no CSV header.
    printf '%s\n' "$payload"
  else
    # Send the JSON object to mono-diagnostics-collector.
    # A failed request is logged to stderr; the logger keeps running.
    if ! curl -fsS -m 5 -o /dev/null \
      -H "X-Diag-Token: $DIAG_TOKEN" \
      -H "Content-Type: application/json" \
      --data-binary "$payload" \
      "http://$HOST/d"; then
      echo "Sensor upload failed at $timestamp" >&2
    fi
  fi

  sleep "$INTERVAL"
done
```