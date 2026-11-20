package core

import "regexp"

var diskRowPattern = regexp.MustCompile(`^.+?\s+(\d+)\s+(\d+)\s+(\d+)\s+\d+%\s+(/.*)$`)

func parseResourceDisks(rows []string) (ResourceDisk, []ResourceDisk) {
	var root ResourceDisk
	disks := make([]ResourceDisk, 0)
	seen := make(map[string]bool)
	for _, row := range rows {
		parts := diskRowPattern.FindStringSubmatch(row)
		if len(parts) != 5 || seen[parts[4]] {
			continue
		}
		total, totalErr := kbBytes(parts[1])
		used, usedErr := kbBytes(parts[2])
		available, availableErr := kbBytes(parts[3])
		if totalErr != nil || usedErr != nil || availableErr != nil || total == 0 || used > total || available > total {
			continue
		}
		disk := ResourceDisk{Total: total, Used: used, Available: available, Mount: parts[4]}
		if disk.Mount == "/" {
			root = disk
		}
		if len(disks) < 64 || disk.Mount == "/" {
			seen[disk.Mount] = true
			disks = append(disks, disk)
		}
	}
	return root, disks
}
