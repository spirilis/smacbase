# smacbase
SMac NPI implementation written in Go, for use with smac_npi TI-RTOS firmware

Notes for RPi3:
Add the following to /boot/config.txt:
dtoverlay=pi3-disable-bt
enable_uart=1

Disable agetty using "systemctl disable serial-getty@ttyAMA0"
