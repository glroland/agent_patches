import { Router } from 'express';
import * as manualRunsController from '../controllers/manualRunsController.js';

const router = Router();

router.post('/:id/result', manualRunsController.submitResult);

export default router;
