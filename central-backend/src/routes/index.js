import { Router } from 'express';
import agentsRouter from './agents.js';
import adminRouter from './admin.js';
import intelligenceRouter from './intelligence.js';
import approvalsRouter from './approvals.js';
import issuesRouter from './issues.js';
import dashboardRouter from './dashboard.js';
import summaryRouter from './summary.js';
import chatRouter from './chat.js';

const router = Router();

router.get('/health', (req, res) => res.json({ status: 'ok' }));

router.use('/agents', agentsRouter);
router.use('/admin', adminRouter);
router.use('/intelligence', intelligenceRouter);
router.use('/approvals', approvalsRouter);
router.use('/issues', issuesRouter);
router.use('/dashboard', dashboardRouter);
router.use('/summary', summaryRouter);
router.use('/chat', chatRouter);

export default router;
