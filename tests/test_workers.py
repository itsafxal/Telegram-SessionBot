import json
import subprocess
import sys
import unittest


class TestWorkerIPC(unittest.TestCase):
    def test_pyrofork_worker_qr_unsupported(self):
        proc = subprocess.Popen(
            [sys.executable, "-u", "workers/pyrofork/worker.py"],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )

        try:
            # Send QR request
            req = {"id": "test-1", "action": "start_qr", "payload": {}}
            proc.stdin.write(json.dumps(req) + "\n")
            proc.stdin.flush()

            resp_line = proc.stdout.readline()
            resp = json.loads(resp_line)

            self.assertEqual(resp["id"], "test-1")
            self.assertEqual(resp["status"], "error")
            self.assertIn("not supported", resp["error"].lower())

            # Send cancel
            cancel_req = {"id": "test-2", "action": "cancel", "payload": {}}
            proc.stdin.write(json.dumps(cancel_req) + "\n")
            proc.stdin.flush()

            cancel_resp = json.loads(proc.stdout.readline())
            self.assertEqual(cancel_resp["status"], "ok")
        finally:
            proc.terminate()
            proc.wait(timeout=2)

    def test_telethon_worker_init_and_cancel(self):
        proc = subprocess.Popen(
            [sys.executable, "-u", "workers/telethon/worker.py"],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )

        try:
            # Send cancel
            cancel_req = {"id": "test-tele-1", "action": "cancel", "payload": {}}
            proc.stdin.write(json.dumps(cancel_req) + "\n")
            proc.stdin.flush()

            cancel_resp = json.loads(proc.stdout.readline())
            self.assertEqual(cancel_resp["status"], "ok")
        finally:
            proc.terminate()
            proc.wait(timeout=2)


if __name__ == "__main__":
    unittest.main()

