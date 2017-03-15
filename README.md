# smacbase
SMac NPI implementation written in Go, for use with smac_npi TI-RTOS firmware

Notes for RPi3:
Add the following to /boot/config.txt:
```
dtoverlay=pi3-disable-bt
enable_uart=1
```

Disable agetty using `systemctl disable serial-getty@ttyAMA0`

Control the Pi3 hat reset line using gpio26 with active_low=1:
```
cd /sys/class/gpio
echo 26 > export
cd gpio26
echo 1 > active_low
echo out > direction
echo 0 > value # Release the RESET line (so JTAG can control it externally)
echo 1 > value # Drive RESET low to reset the chip
echo 0 > value # Release RESET to allow CC1310 MCU to boot
```
