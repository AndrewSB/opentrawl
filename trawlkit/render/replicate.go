package render

import "strings"

// WriteArchiveReplicationStarted writes one line on standard error as each
// archive starts copying, because a replication can take minutes.
func (output TrawlCommandOutput) WriteArchiveReplicationStarted(trawlerCommandName string) error {
	return writeLine(output.standardError, trawlerCommandName+" replicating…")
}

// WriteArchiveReplication names every archive that reached the replica.
func (output TrawlCommandOutput) WriteArchiveReplication(
	replicatedTrawlerCommandNamesInOrder []string,
	replicaDestinationDisplay string,
) error {
	writer := output.standardOutput
	return writeLines(writer, Wrap(
		"Replicated "+strings.Join(replicatedTrawlerCommandNamesInOrder, ", ")+" to "+replicaDestinationDisplay+".",
		OutputWidth(writer),
	))
}

// Replication failures.

func ReplicaDestinationMustBeHostAndAbsolutePath() CommandFailure {
	return commandIsWrong("--to must be USER@HOST:/absolute/path, using a simple SSH host or alias and a path other than /.")
}

func NoSelectedTrawlerHasArchiveToReplicate() CommandFailure {
	return archiveOrOperationFailed("None of the selected trawlers has an archive to replicate.")
}

func ReplicationNeedsProgramOnThisComputer(programName string) CommandFailure {
	return archiveOrOperationFailed("Replication needs " + programName + " on this computer.")
}

func ReplicationNeedsProgramOnReplicaHost(programName string) CommandFailure {
	return archiveOrOperationFailed("Replication needs " + programName + " on the replica host.")
}

func TrawlerArchiveLocationIsUnknown(trawlerDisplayName string) CommandFailure {
	return archiveOrOperationFailed("The " + trawlerDisplayName + " archive location is unknown.")
}

func TrawlerArchiveIsOutsideStateRoot(trawlerDisplayName string) CommandFailure {
	return archiveOrOperationFailed("The " + trawlerDisplayName + " archive is outside the OpenTrawl state root, so it has no place in a replica.")
}

// ReplicationStepFailed names the step, such as "replicate whatsapp"; what
// the failed program said goes to the run log.
func ReplicationStepFailed(replicationStep string) CommandFailure {
	return archiveOrOperationFailed("Could not " + replicationStep + ".")
}

func ReplicaFailedIntegrityCheck(trawlerCommandName string) CommandFailure {
	return archiveOrOperationFailed("The " + trawlerCommandName + " replica failed the SQLite integrity check. The previous replica is kept.")
}

func ReplicationTimedOut() CommandFailure {
	return archiveOrOperationFailed("Replication timed out.")
}

func ReplicationIsAlreadyRunning() CommandFailure {
	return archiveOrOperationFailed("OpenTrawl is already replicating.")
}
