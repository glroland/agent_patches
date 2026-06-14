import { Router } from 'express';
import agentsRouter from './agents.js';
import approvalsRouter from './approvals.js';
import issuesRouter from './issues.js';
import dashboardRouter from './dashboard.js';
import summaryRouter from './summary.js';

const router = Router();

router.get('/health', (req, res) => res.json({ status: 'ok' }));

router.use('/agents', agentsRouter);
router.use('/approvals', approvalsRouter);
router.use('/issues', issuesRouter);
router.use('/dashboard', dashboardRouter);
router.use('/summary', summaryRouter);

export default router;
