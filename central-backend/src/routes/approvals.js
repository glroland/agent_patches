import { Router } from 'express';
import * as approvalsController from '../controllers/approvalsController.js';

const router = Router();

router.get('/', approvalsController.listApprovals);
router.post('/:id/decision', approvalsController.decideApproval);

export default router;
