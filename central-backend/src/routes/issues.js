import { Router } from 'express';
import * as issuesController from '../controllers/issuesController.js';

const router = Router();

router.get('/', issuesController.listIssues);
router.post('/:id/resolve', issuesController.resolveIssue);

export default router;
